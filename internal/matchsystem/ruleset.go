package matchsystem

import "sort"

type CandidateFilterFlag uint8

const CandidateFilterActive CandidateFilterFlag = 1 << iota

func (f CandidateFilterFlag) Has(flag CandidateFilterFlag) bool { return f&flag != 0 }
func (f CandidateFilterFlag) Active() bool                      { return f == 0 || f.Has(CandidateFilterActive) }

type CandidateFilterSource uint8

const (
	CandidateFilterFromIndex CandidateFilterSource = iota
	CandidateFilterFromCandidates
)

// CandidateFilterContext exposes domain-neutral pool lookups to custom filters.
type CandidateFilterContext struct {
	Pool *MatchPool
	Seed *Ticket
	Now  int64
}

func (c CandidateFilterContext) Ticket(docID uint32) (*Ticket, bool) {
	if c.Pool == nil {
		return nil, false
	}
	ticket, ok := c.Pool.ticketsByDocID[docID]
	return ticket, ok
}

func (c CandidateFilterContext) NumericRange(field string, min, max int64) TicketSet {
	if c.Pool == nil {
		return NewTicketSet()
	}
	return c.Pool.numericRange(field, min, max)
}

func (c CandidateFilterContext) EstimateNumericRange(field string, min, max int64) int {
	if c.Pool == nil {
		return 0
	}
	return c.Pool.estimateNumericRange(field, min, max)
}

func (c CandidateFilterContext) AttributeEquals(field, value string) TicketSet {
	if c.Pool == nil {
		return NewTicketSet()
	}
	return c.Pool.attributeEquals(field, value)
}

func (c CandidateFilterContext) EstimateAttributeEquals(field, value string) int {
	if c.Pool == nil {
		return 0
	}
	return c.Pool.estimateAttributeEquals(field, value)
}

type CandidateFilterEstimate struct {
	EstimatedOut  int
	Cost          int
	SupportsIndex bool
	PreferIndex   bool
}

type CandidateFilter interface {
	CandidateFlags() CandidateFilterFlag
	EstimateCandidates(ctx CandidateFilterContext, candidates TicketSet) CandidateFilterEstimate
	FilterCandidates(ctx CandidateFilterContext, candidates TicketSet, source CandidateFilterSource) TicketSet
}

type GroupEvaluatorFlag uint8

const (
	GroupEvaluatorJoin GroupEvaluatorFlag = 1 << iota
	GroupEvaluatorStart
	GroupEvaluatorForceStart
)

func (f GroupEvaluatorFlag) Has(flag GroupEvaluatorFlag) bool { return f&flag != 0 }

type GroupEvaluatorContext struct {
	Seed  *Ticket
	Now   int64
	Phase GroupEvaluatorFlag
}

type GroupEvaluator interface {
	EvaluatorFlags() GroupEvaluatorFlag
	AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool
}

type GroupCondition interface {
	MatchGroup(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool
}

type GroupConditionFunc func(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool

func (f GroupConditionFunc) MatchGroup(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	return f != nil && f(ctx, group, candidate)
}

type CandidateFilterOnly struct{ Filter CandidateFilter }

func (r CandidateFilterOnly) CandidateFlags() CandidateFilterFlag {
	if r.Filter == nil {
		return 0
	}
	return r.Filter.CandidateFlags()
}
func (r CandidateFilterOnly) EstimateCandidates(ctx CandidateFilterContext, candidates TicketSet) CandidateFilterEstimate {
	if r.Filter == nil {
		return CandidateFilterEstimate{EstimatedOut: candidates.Len(), Cost: 1}
	}
	return r.Filter.EstimateCandidates(ctx, candidates)
}
func (r CandidateFilterOnly) FilterCandidates(ctx CandidateFilterContext, candidates TicketSet, source CandidateFilterSource) TicketSet {
	if r.Filter == nil {
		return candidates.Clone()
	}
	return r.Filter.FilterCandidates(ctx, candidates, source)
}

type GroupEvaluatorOnly struct{ Evaluator GroupEvaluator }

func (r GroupEvaluatorOnly) EvaluatorFlags() GroupEvaluatorFlag {
	if r.Evaluator == nil {
		return 0
	}
	return r.Evaluator.EvaluatorFlags()
}
func (r GroupEvaluatorOnly) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	return r.Evaluator == nil || !evaluatorApplies(r.Evaluator, ctx.Phase) || r.Evaluator.AllowJoin(ctx, group, candidate)
}

type AllGroupEvaluator struct {
	Flags    GroupEvaluatorFlag
	Children []GroupEvaluator
}

func AllEvaluators(children ...GroupEvaluator) AllGroupEvaluator {
	return AllGroupEvaluator{Children: children}
}
func (r AllGroupEvaluator) EvaluatorFlags() GroupEvaluatorFlag {
	if r.Flags != 0 {
		return r.Flags
	}
	return evaluatorFlags(r.Children)
}
func (r AllGroupEvaluator) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	for _, child := range r.Children {
		if evaluatorApplies(child, ctx.Phase) && !child.AllowJoin(ctx, group, candidate) {
			return false
		}
	}
	return true
}

type AnyGroupEvaluator struct {
	Flags    GroupEvaluatorFlag
	Children []GroupEvaluator
}

func AnyEvaluators(children ...GroupEvaluator) AnyGroupEvaluator {
	return AnyGroupEvaluator{Children: children}
}
func (r AnyGroupEvaluator) EvaluatorFlags() GroupEvaluatorFlag {
	if r.Flags != 0 {
		return r.Flags
	}
	return evaluatorFlags(r.Children)
}
func (r AnyGroupEvaluator) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	matchedPhase := false
	for _, child := range r.Children {
		if !evaluatorApplies(child, ctx.Phase) {
			continue
		}
		matchedPhase = true
		if child.AllowJoin(ctx, group, candidate) {
			return true
		}
	}
	return !matchedPhase
}

type NotGroupEvaluator struct {
	Flags GroupEvaluatorFlag
	Child GroupEvaluator
}

func NotEvaluator(child GroupEvaluator) NotGroupEvaluator { return NotGroupEvaluator{Child: child} }
func (r NotGroupEvaluator) EvaluatorFlags() GroupEvaluatorFlag {
	if r.Flags != 0 {
		return r.Flags
	}
	if r.Child == nil {
		return 0
	}
	return r.Child.EvaluatorFlags()
}
func (r NotGroupEvaluator) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	return !evaluatorApplies(r.Child, ctx.Phase) || !r.Child.AllowJoin(ctx, group, candidate)
}

type WhenGroupEvaluator struct {
	Flags     GroupEvaluatorFlag
	Condition GroupCondition
	Then      GroupEvaluator
	Else      GroupEvaluator
}

func WhenEvaluator(condition GroupCondition, thenEvaluator, elseEvaluator GroupEvaluator) WhenGroupEvaluator {
	return WhenGroupEvaluator{Condition: condition, Then: thenEvaluator, Else: elseEvaluator}
}
func (r WhenGroupEvaluator) EvaluatorFlags() GroupEvaluatorFlag {
	if r.Flags != 0 {
		return r.Flags
	}
	return evaluatorFlags([]GroupEvaluator{r.Then, r.Else})
}
func (r WhenGroupEvaluator) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	branch := r.Else
	if r.Condition != nil && r.Condition.MatchGroup(ctx, group, candidate) {
		branch = r.Then
	}
	return !evaluatorApplies(branch, ctx.Phase) || branch.AllowJoin(ctx, group, candidate)
}

type FuncCandidateFilter struct {
	FilterFlags CandidateFilterFlag
	EstimateFn  func(ctx CandidateFilterContext, candidates TicketSet) CandidateFilterEstimate
	FilterFn    func(ctx CandidateFilterContext, candidates TicketSet, source CandidateFilterSource) TicketSet
}

func (r FuncCandidateFilter) CandidateFlags() CandidateFilterFlag { return r.FilterFlags }
func (r FuncCandidateFilter) EstimateCandidates(ctx CandidateFilterContext, candidates TicketSet) CandidateFilterEstimate {
	if r.EstimateFn == nil {
		return CandidateFilterEstimate{EstimatedOut: candidates.Len(), Cost: 1}
	}
	return r.EstimateFn(ctx, candidates)
}
func (r FuncCandidateFilter) FilterCandidates(ctx CandidateFilterContext, candidates TicketSet, source CandidateFilterSource) TicketSet {
	if r.FilterFn == nil {
		return candidates.Clone()
	}
	return r.FilterFn(ctx, candidates, source)
}

type FuncGroupEvaluator struct {
	EvaluatorFlagsValue GroupEvaluatorFlag
	AllowFn             func(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool
}

func (r FuncGroupEvaluator) EvaluatorFlags() GroupEvaluatorFlag { return r.EvaluatorFlagsValue }
func (r FuncGroupEvaluator) AllowJoin(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	return r.AllowFn == nil || r.AllowFn(ctx, group, candidate)
}

type CandidateScoreFunc func(seed, candidate *Ticket, now int64) float64

type matchRules interface {
	CandidateSet(pool *MatchPool, seed *Ticket, now int64) TicketSet
	CanJoinGroup(group []*Ticket, candidate *Ticket, now int64) bool
	CanStartGroup(group []*Ticket, now int64) bool
	ShouldForceStart(seed *Ticket, now int64) bool
	ScoreCandidate(seed, candidate *Ticket, now int64) float64
}

type RuleSet struct {
	filters          []CandidateFilter
	evaluators       []GroupEvaluator
	candidateScoreFn CandidateScoreFunc
}

func NewRuleSet(parts ...any) *RuleSet { return (&RuleSet{}).Use(parts...) }
func (r *RuleSet) Use(parts ...any) *RuleSet {
	for _, part := range parts {
		if filter, ok := part.(CandidateFilter); ok && filter != nil {
			r.filters = append(r.filters, filter)
		}
		if evaluator, ok := part.(GroupEvaluator); ok && evaluator != nil {
			r.evaluators = append(r.evaluators, evaluator)
		}
	}
	return r
}
func (r *RuleSet) WithCandidateScore(score CandidateScoreFunc) *RuleSet {
	r.candidateScoreFn = score
	return r
}
func (r *RuleSet) Filters() []CandidateFilter { return append([]CandidateFilter(nil), r.filters...) }
func (r *RuleSet) Evaluators() []GroupEvaluator {
	return append([]GroupEvaluator(nil), r.evaluators...)
}

func (r *RuleSet) CandidateSet(pool *MatchPool, seed *Ticket, now int64) TicketSet {
	ctx := CandidateFilterContext{Pool: pool, Seed: seed, Now: now}
	candidates := pool.allSet()
	remaining := r.activeFilters()
	for len(remaining) != 0 {
		nextIndex, estimate := bestCandidateFilter(ctx, candidates, remaining)
		filter := remaining[nextIndex]
		source := CandidateFilterFromCandidates
		if estimate.SupportsIndex && (estimate.PreferIndex || estimate.EstimatedOut < candidates.Len()) {
			source = CandidateFilterFromIndex
		}
		filtered := filter.FilterCandidates(ctx, candidates, source)
		if source == CandidateFilterFromIndex {
			candidates = candidates.And(filtered)
		} else {
			candidates = filtered
		}
		if candidates.Len() == 0 {
			break
		}
		remaining = append(remaining[:nextIndex], remaining[nextIndex+1:]...)
	}
	return candidates
}

func (r *RuleSet) CanJoinGroup(group []*Ticket, candidate *Ticket, now int64) bool {
	if len(group) == 0 || candidate == nil {
		return false
	}
	ctx := GroupEvaluatorContext{Seed: group[0], Now: now, Phase: GroupEvaluatorJoin}
	for _, evaluator := range r.evaluators {
		if evaluatorApplies(evaluator, ctx.Phase) && !evaluator.AllowJoin(ctx, group, candidate) {
			return false
		}
	}
	return true
}

func (r *RuleSet) CanStartGroup(group []*Ticket, now int64) bool {
	if len(group) == 0 {
		return false
	}
	ctx := GroupEvaluatorContext{Seed: group[0], Now: now, Phase: GroupEvaluatorStart}
	hasStartRule := false
	for _, evaluator := range r.evaluators {
		if !evaluatorApplies(evaluator, ctx.Phase) {
			continue
		}
		hasStartRule = true
		if !evaluator.AllowJoin(ctx, group, nil) {
			return false
		}
	}
	return hasStartRule
}

func (r *RuleSet) ShouldForceStart(seed *Ticket, now int64) bool {
	if seed == nil {
		return false
	}
	ctx := GroupEvaluatorContext{Seed: seed, Now: now, Phase: GroupEvaluatorForceStart}
	for _, evaluator := range r.evaluators {
		if evaluatorApplies(evaluator, ctx.Phase) && evaluator.AllowJoin(ctx, []*Ticket{seed}, nil) {
			return true
		}
	}
	return false
}

func (r *RuleSet) ScoreCandidate(seed, candidate *Ticket, now int64) float64 {
	if r.candidateScoreFn != nil {
		return r.candidateScoreFn(seed, candidate, now)
	}
	return float64(now-candidate.CreatedAt)/1000 - float64(candidate.DocID)*0.000001
}

func (r *RuleSet) activeFilters() []CandidateFilter {
	out := make([]CandidateFilter, 0, len(r.filters))
	for _, filter := range r.filters {
		if filter.CandidateFlags().Active() {
			out = append(out, filter)
		}
	}
	return out
}

func bestCandidateFilter(ctx CandidateFilterContext, candidates TicketSet, filters []CandidateFilter) (int, CandidateFilterEstimate) {
	type estimatedFilter struct {
		index    int
		estimate CandidateFilterEstimate
	}
	estimated := make([]estimatedFilter, 0, len(filters))
	for i, filter := range filters {
		estimate := filter.EstimateCandidates(ctx, candidates)
		if estimate.Cost <= 0 {
			estimate.Cost = 1
		}
		if estimate.EstimatedOut < 0 {
			estimate.EstimatedOut = candidates.Len()
		}
		estimated = append(estimated, estimatedFilter{index: i, estimate: estimate})
	}
	sort.Slice(estimated, func(i, j int) bool {
		left, right := estimated[i].estimate, estimated[j].estimate
		if left.EstimatedOut != right.EstimatedOut {
			return left.EstimatedOut < right.EstimatedOut
		}
		return left.Cost < right.Cost
	})
	best := estimated[0]
	return best.index, best.estimate
}

func evaluatorApplies(evaluator GroupEvaluator, phase GroupEvaluatorFlag) bool {
	return evaluator != nil && evaluator.EvaluatorFlags().Has(phase)
}

func evaluatorFlags(evaluators []GroupEvaluator) GroupEvaluatorFlag {
	var flags GroupEvaluatorFlag
	for _, evaluator := range evaluators {
		if evaluator != nil {
			flags |= evaluator.EvaluatorFlags()
		}
	}
	return flags
}
