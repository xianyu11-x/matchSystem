package matchsystem

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

// GroupEvaluator implementations and CandidateScoreFunc callbacks run on the
// owning PhysicalNode goroutine. They must be immutable, side-effect free, and
// must not re-enter or mutate the owning PhysicalNode.

type GroupCondition interface {
	MatchGroup(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool
}
type GroupConditionFunc func(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool

func (f GroupConditionFunc) MatchGroup(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
	return f != nil && f(ctx, group, candidate)
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
	matched := false
	for _, child := range r.Children {
		if !evaluatorApplies(child, ctx.Phase) {
			continue
		}
		matched = true
		if child.AllowJoin(ctx, group, candidate) {
			return true
		}
	}
	return !matched
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
	CanJoinGroup(group []*Ticket, candidate *Ticket, now int64) bool
	CanStartGroup(group []*Ticket, now int64) bool
	ShouldForceStart(seed *Ticket, now int64) bool
	ScoreCandidate(seed, candidate *Ticket, now int64) float64
}

type RuleSet struct {
	evaluators       []GroupEvaluator
	candidateScoreFn CandidateScoreFunc
}

func NewRuleSet(evaluators ...GroupEvaluator) *RuleSet { return (&RuleSet{}).Use(evaluators...) }
func (r *RuleSet) clone() *RuleSet {
	if r == nil {
		return NewRuleSet()
	}
	return &RuleSet{
		evaluators:       append([]GroupEvaluator(nil), r.evaluators...),
		candidateScoreFn: r.candidateScoreFn,
	}
}
func (r *RuleSet) Use(evaluators ...GroupEvaluator) *RuleSet {
	for _, evaluator := range evaluators {
		if evaluator != nil {
			r.evaluators = append(r.evaluators, evaluator)
		}
	}
	return r
}
func (r *RuleSet) WithCandidateScore(score CandidateScoreFunc) *RuleSet {
	r.candidateScoreFn = score
	return r
}
func (r *RuleSet) Evaluators() []GroupEvaluator {
	return append([]GroupEvaluator(nil), r.evaluators...)
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
	hasStart := false
	for _, evaluator := range r.evaluators {
		if evaluatorApplies(evaluator, ctx.Phase) {
			hasStart = true
			if !evaluator.AllowJoin(ctx, group, nil) {
				return false
			}
		}
	}
	return hasStart
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
