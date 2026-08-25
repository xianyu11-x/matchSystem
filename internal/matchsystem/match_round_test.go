package matchsystem

import "errors"

func produceTestRound(node *LogicalNode, now int64, facts Facts) ([]Match, error) {
	if err := node.BeginMatchRound(now); err != nil {
		return nil, err
	}
	matches := make([]Match, 0)
	var roundErrors []error
	for node.hasUntriedSeed() {
		match, err := node.ProduceMatch(facts)
		if err != nil {
			roundErrors = append(roundErrors, err)
		}
		if match != nil {
			matches = append(matches, *match)
		}
	}
	return matches, errors.Join(roundErrors...)
}

func produceTestMatch(node *LogicalNode, now int64, facts Facts) (*Match, error) {
	if err := node.BeginMatchRound(now); err != nil {
		return nil, err
	}
	return node.ProduceMatch(facts)
}
