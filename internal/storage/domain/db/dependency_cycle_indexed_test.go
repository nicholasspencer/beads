package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// legacyDerivedUnionCycleQuery is the shape this change replaced: the
// recursion joined against a derived UNION of both dependency tables, which
// carries no index. It lives here only so the timing witness can measure what
// it cost.
const legacyDerivedUnionCycleQuery = `
	WITH RECURSIVE reachable(node) AS (
		SELECT ?
		UNION
		SELECT d.depends_on_id
		FROM reachable r
		JOIN (
			SELECT issue_id, COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) AS depends_on_id FROM dependencies WHERE type IN ('blocks', 'conditional-blocks', 'parent-child')
			UNION
			SELECT issue_id, COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external) AS depends_on_id FROM wisp_dependencies WHERE type IN ('blocks', 'conditional-blocks', 'parent-child')
		) d ON d.issue_id = r.node
	)
	SELECT COUNT(*) FROM reachable WHERE node = ?
`

// issueUseCaseOver builds the full use-case stack over an arbitrary Runner so
// a test can wrap the suite's runner. s.issueUseCase() delegates here.
func issueUseCaseOver(runner Runner) domain.IssueUseCase {
	labelUC := domain.NewLabelUseCase(NewLabelSQLRepository(runner))
	depUC := domain.NewDependencyUseCase(NewDependencySQLRepository(runner))
	return domain.NewIssueUseCase(
		NewIssueSQLRepository(runner),
		NewDependencySQLRepository(runner),
		NewLabelSQLRepository(runner),
		NewChildCounterSQLRepository(runner),
		NewCommentSQLRepository(runner),
		NewConfigSQLRepository(runner),
		NewEventsSQLRepository(runner),
		labelUC,
		depUC,
	)
}

// recordingRunner wraps a Runner and keeps every statement text it is asked
// to run, so a test can assert which queries a use case did NOT issue.
type recordingRunner struct {
	inner Runner
	mu    sync.Mutex
	seen  []string
}

func (r *recordingRunner) record(query string) {
	r.mu.Lock()
	r.seen = append(r.seen, query)
	r.mu.Unlock()
}

// matching returns every recorded statement containing needle.
func (r *recordingRunner) matching(needle string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, q := range r.seen {
		if strings.Contains(q, needle) {
			out = append(out, q)
		}
	}
	return out
}

func (r *recordingRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.record(query)
	return r.inner.ExecContext(ctx, query, args...)
}

func (r *recordingRunner) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.record(query)
	return r.inner.QueryContext(ctx, query, args...)
}

func (r *recordingRunner) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.record(query)
	return r.inner.QueryRowContext(ctx, query, args...)
}

// chainGraphPlan builds a plan of nodeCount tasks chained by blocking edges.
func chainGraphPlan(nodeCount int) domain.GraphPlan {
	plan := domain.GraphPlan{}
	for i := 0; i < nodeCount; i++ {
		key := fmt.Sprintf("step-%02d", i)
		plan.Nodes = append(plan.Nodes, domain.GraphNode{
			Key: key,
			Issue: &types.Issue{
				Title:     fmt.Sprintf("Step %d", i),
				IssueType: types.TypeTask,
				Priority:  2,
			},
		})
		if i > 0 {
			plan.Edges = append(plan.Edges, domain.GraphEdge{
				FromKey: fmt.Sprintf("step-%02d", i-1),
				ToKey:   key,
				Type:    types.DepBlocks,
			})
		}
	}
	return plan
}

func (s *testSuite) TestApplyGraphSkipsPerEdgeCycleProbe() {
	s.resetMintConfig("bd", "")
	rec := &recordingRunner{inner: s.Runner()}

	res, err := issueUseCaseOver(rec).ApplyIssueGraph(s.Ctx(), chainGraphPlan(12), "tester")
	s.Require().NoError(err)
	s.Require().Len(res.IDs, 12)

	probes := rec.matching("WITH RECURSIVE reachable(node)")
	s.Empty(probes, "a plan-local graph apply must issue no per-edge reachability probe, got %d", len(probes))
	// Control: the hierarchy guard is NOT declared validated, so its own
	// recursive walk must still be issued. Without this the assertion above
	// would pass on a runner that recorded nothing.
	s.NotEmpty(rec.matching("WITH RECURSIVE ancestors(node)"),
		"the hierarchy ancestry walk must still run per edge")
}

func (s *testSuite) TestApplyGraphStillRefusesAPlanThatClosesACycle() {
	s.resetMintConfig("bd", "")
	plan := chainGraphPlan(4)
	plan.Edges = append(plan.Edges, domain.GraphEdge{
		FromKey: "step-03",
		ToKey:   "step-00",
		Type:    types.DepBlocks,
	})

	_, err := issueUseCaseOver(s.Runner()).ApplyIssueGraph(s.Ctx(), plan, "tester")
	s.Require().Error(err)
	s.Contains(err.Error(), "cycle")
}

func (s *testSuite) TestCycleProbeStaysFastOnADeepChain() {
	ctx := s.Ctx()
	const (
		noiseRows  = 4000
		chainDepth = 30
	)
	s.seedIssues(ctx, "perf-noise", noiseRows)
	s.seedBlockingChain(ctx, "perf-noise", noiseRows)
	s.seedIssues(ctx, "perf-chain", chainDepth)
	s.seedBlockingChain(ctx, "perf-chain", chainDepth)

	head := "perf-chain-0000"
	tail := fmt.Sprintf("perf-chain-%04d", chainDepth-1)

	started := time.Now()
	cycle, err := issueops.WouldCreateSchedulingCycleInTx(ctx, s.Runner(), head, tail, nil)
	indexed := time.Since(started)
	s.Require().NoError(err)
	s.True(cycle, "the chain head must be reachable from its tail")

	var legacyCount int
	startedLegacy := time.Now()
	err = s.Runner().QueryRowContext(ctx, legacyDerivedUnionCycleQuery, tail, head).Scan(&legacyCount)
	legacy := time.Since(startedLegacy)
	s.Require().NoError(err)
	s.Equal(1, legacyCount, "both shapes must agree the head is reachable")

	s.T().Logf("indexed=%s legacy=%s (%d noise rows, depth %d)", indexed, legacy, noiseRows, chainDepth)
	s.Less(indexed, 20*time.Millisecond, "indexed cycle probe took %s", indexed)
	s.Greater(legacy, 4*indexed, "derived-union shape (%s) must be materially slower than the indexed shape (%s)", legacy, indexed)
}

// seedIssues bulk-inserts count issues named <prefix>-0000.. so the timing
// witness can build a graph without paying per-row use-case overhead. Only the
// NOT NULL columns without defaults are named.
func (s *testSuite) seedIssues(ctx context.Context, prefix string, count int) {
	const batch = 500
	for start := 0; start < count; start += batch {
		end := start + batch
		if end > count {
			end = count
		}
		var b strings.Builder
		b.WriteString("INSERT INTO issues (id, title, description, design, acceptance_criteria, notes) VALUES ")
		args := make([]any, 0, (end-start)*2)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteString(", ")
			}
			b.WriteString("(?, ?, '', '', '', '')")
			args = append(args, fmt.Sprintf("%s-%04d", prefix, i), fmt.Sprintf("%s %d", prefix, i))
		}
		_, err := s.Runner().ExecContext(ctx, b.String(), args...)
		s.Require().NoError(err, "seed issues %s [%d,%d)", prefix, start, end)
	}
}

// seedBlockingChain links <prefix>-0001 -> <prefix>-0000 -> ... as blocks
// edges, naming the same columns DependencySQLRepository.Insert writes.
func (s *testSuite) seedBlockingChain(ctx context.Context, prefix string, count int) {
	const batch = 500
	now := time.Now().UTC()
	for start := 1; start < count; start += batch {
		end := start + batch
		if end > count {
			end = count
		}
		var b strings.Builder
		b.WriteString("INSERT INTO dependencies (id, issue_id, depends_on_issue_id, type, created_at, created_by, metadata, thread_id) VALUES ")
		args := make([]any, 0, (end-start)*8)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteString(", ")
			}
			b.WriteString("(?, ?, ?, 'blocks', ?, 'seed', '{}', '')")
			src := fmt.Sprintf("%s-%04d", prefix, i)
			dst := fmt.Sprintf("%s-%04d", prefix, i-1)
			args = append(args, depid.New(src, dst), src, dst, now)
		}
		_, err := s.Runner().ExecContext(ctx, b.String(), args...)
		s.Require().NoError(err, "seed chain %s [%d,%d)", prefix, start, end)
	}
}
