package prefilter

// Expr is the closed candidate prefilter expression interface.
type Expr interface{ expr() }

type lookupExpr struct{ query IndexQuery }
type andExpr struct{ children []Expr }
type orExpr struct{ children []Expr }
type excludeExpr struct{ child Expr }
type ifExpr struct {
	condition          Condition
	thenExpr, elseExpr Expr
}
type noneExpr struct{}

func (*lookupExpr) expr()  {}
func (*andExpr) expr()     {}
func (*orExpr) expr()      {}
func (*excludeExpr) expr() {}
func (*ifExpr) expr()      {}
func (*noneExpr) expr()    {}

func Lookup(query IndexQuery) Expr { return &lookupExpr{query: query} }
func And(children ...Expr) Expr    { return &andExpr{children: append([]Expr(nil), children...)} }
func Or(children ...Expr) Expr     { return &orExpr{children: append([]Expr(nil), children...)} }
func Exclude(child Expr) Expr      { return &excludeExpr{child: child} }
func If(condition Condition, thenExpr, elseExpr Expr) Expr {
	return &ifExpr{condition: condition, thenExpr: thenExpr, elseExpr: elseExpr}
}
func None() Expr { return &noneExpr{} }
