package mvel

type valueKind uint8

const (
	decimalKind valueKind = iota + 1
	stringKind
	predicateKind
)

// Value is one typed evaluator input. Construct values with Number or Text.
type Value struct {
	kind    valueKind
	decimal Decimal
	text    string
}

// Number constructs an exact decimal binding.
func Number(decimal Decimal) Value {
	return Value{kind: decimalKind, decimal: decimal}
}

// Text constructs a string binding. Evaluation enforces the fixed string budget.
func Text(text string) Value {
	return Value{kind: stringKind, text: text}
}

// Bindings maps allowlisted expression identifiers to typed values.
type Bindings map[string]Value

type runtimeValue struct {
	kind      valueKind
	decimal   Decimal
	text      string
	predicate bool
}
