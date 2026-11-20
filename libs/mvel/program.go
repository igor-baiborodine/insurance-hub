package mvel

import "unicode/utf8"

// Predicate is an immutable compiled Boolean expression safe for concurrent evaluation.
type Predicate struct {
	root       expressionNode
	references map[string]valueKind
}

// DecimalExpression is an immutable compiled decimal expression safe for concurrent evaluation.
type DecimalExpression struct {
	root       expressionNode
	references map[string]valueKind
}

// CompilePredicate compiles one expression that must return a predicate.
func CompilePredicate(source string) (*Predicate, error) {
	root, references, err := parseExpression(source, predicateKind)
	if err != nil {
		return nil, err
	}
	return &Predicate{root: root, references: references}, nil
}

// CompileDecimal compiles one expression that must return an exact decimal.
func CompileDecimal(source string) (*DecimalExpression, error) {
	root, references, err := parseExpression(source, decimalKind)
	if err != nil {
		return nil, err
	}
	return &DecimalExpression{root: root, references: references}, nil
}

// Evaluate evaluates the predicate against exactly the referenced bindings.
func (predicate *Predicate) Evaluate(bindings Bindings) (bool, error) {
	if predicate == nil {
		return false, sourceError(ErrSyntax, -1, "predicate is nil")
	}
	if err := validateBindings(bindings, predicate.references); err != nil {
		return false, err
	}
	steps := 0
	result, err := predicate.root.evaluate(bindings, &steps)
	if err != nil {
		return false, err
	}
	return result.predicate, nil
}

// Evaluate evaluates the decimal expression against exactly the referenced bindings.
func (expression *DecimalExpression) Evaluate(bindings Bindings) (Decimal, error) {
	if expression == nil {
		return Decimal{}, sourceError(ErrSyntax, -1, "decimal expression is nil")
	}
	if err := validateBindings(bindings, expression.references); err != nil {
		return Decimal{}, err
	}
	steps := 0
	result, err := expression.root.evaluate(bindings, &steps)
	if err != nil {
		return Decimal{}, err
	}
	return result.decimal, nil
}

func validateBindings(bindings Bindings, references map[string]valueKind) error {
	if len(bindings) > maxBindings {
		return &evaluatorError{
			category: ErrLimit,
			position: -1,
			reason:   "binding count exceeded",
		}
	}
	for name, kind := range references {
		binding, ok := bindings[name]
		if !ok {
			return bindingError(name, "binding is missing")
		}
		if binding.kind != kind {
			return bindingError(name, "binding has the wrong type")
		}
		if kind == stringKind {
			if !utf8.ValidString(binding.text) {
				return bindingError(name, "string is not valid UTF-8")
			}
			if len(binding.text) > maxStringBytes {
				return bindingLimitError(name, "string byte limit exceeded")
			}
		}
		if kind == decimalKind {
			if _, err := checkedDecimal(binding.decimal.value); err != nil {
				return bindingLimitError(name, "decimal limit exceeded")
			}
		}
	}
	for name := range bindings {
		if _, ok := references[name]; !ok {
			return bindingError(name, "binding is not referenced")
		}
	}
	return nil
}
