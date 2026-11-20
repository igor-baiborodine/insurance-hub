package mvel

type expressionNode interface {
	resultKind() valueKind
	evaluate(Bindings, *int) (runtimeValue, error)
	references(map[string]valueKind)
}

type literalNode struct {
	value runtimeValue
}

func (node *literalNode) resultKind() valueKind {
	return node.value.kind
}

func (node *literalNode) evaluate(_ Bindings, steps *int) (runtimeValue, error) {
	if err := useStep(steps); err != nil {
		return runtimeValue{}, err
	}
	return node.value, nil
}

func (node *literalNode) references(_ map[string]valueKind) {}

type identifierNode struct {
	name string
	kind valueKind
}

func (node *identifierNode) resultKind() valueKind {
	return node.kind
}

func (node *identifierNode) evaluate(bindings Bindings, steps *int) (runtimeValue, error) {
	if err := useStep(steps); err != nil {
		return runtimeValue{}, err
	}
	binding := bindings[node.name]
	return runtimeValue{kind: binding.kind, decimal: binding.decimal, text: binding.text}, nil
}

func (node *identifierNode) references(found map[string]valueKind) {
	found[node.name] = node.kind
}

type binaryNode struct {
	operator    tokenKind
	left, right expressionNode
	kind        valueKind
}

func (node *binaryNode) resultKind() valueKind {
	return node.kind
}

func (node *binaryNode) evaluate(bindings Bindings, steps *int) (runtimeValue, error) {
	if err := useStep(steps); err != nil {
		return runtimeValue{}, err
	}
	left, err := node.left.evaluate(bindings, steps)
	if err != nil {
		return runtimeValue{}, err
	}
	right, err := node.right.evaluate(bindings, steps)
	if err != nil {
		return runtimeValue{}, err
	}

	switch node.operator {
	case tokenAdd:
		result, addErr := add(left.decimal, right.decimal)
		return runtimeValue{kind: decimalKind, decimal: result}, addErr
	case tokenMultiply:
		result, multiplyErr := multiply(left.decimal, right.decimal)
		return runtimeValue{kind: decimalKind, decimal: result}, multiplyErr
	case tokenEqual:
		return runtimeValue{kind: predicateKind, predicate: left.text == right.text}, nil
	case tokenGreater:
		return runtimeValue{
			kind: predicateKind, predicate: greaterThan(left.decimal, right.decimal),
		}, nil
	default:
		return runtimeValue{}, sourceError(
			ErrUnsupported,
			-1,
			"operator is not implemented",
		)
	}
}

func (node *binaryNode) references(found map[string]valueKind) {
	node.left.references(found)
	node.right.references(found)
}

type ternaryNode struct {
	condition, truthy, falsy expressionNode
}

func (node *ternaryNode) resultKind() valueKind {
	return decimalKind
}

func (node *ternaryNode) evaluate(bindings Bindings, steps *int) (runtimeValue, error) {
	if err := useStep(steps); err != nil {
		return runtimeValue{}, err
	}
	condition, err := node.condition.evaluate(bindings, steps)
	if err != nil {
		return runtimeValue{}, err
	}
	if condition.predicate {
		return node.truthy.evaluate(bindings, steps)
	}
	return node.falsy.evaluate(bindings, steps)
}

func (node *ternaryNode) references(found map[string]valueKind) {
	node.condition.references(found)
	node.truthy.references(found)
	node.falsy.references(found)
}

type parser struct {
	tokens []token
	index  int
	nodes  int
}

func parseExpression(
	source string,
	expected valueKind,
) (expressionNode, map[string]valueKind, error) {
	tokens, err := tokenize(source)
	if err != nil {
		return nil, nil, err
	}
	state := &parser{tokens: tokens}
	root, err := state.parseTernary(0)
	if err != nil {
		return nil, nil, err
	}
	if state.current().kind != tokenEOF {
		return nil, nil, sourceError(
			ErrSyntax, state.current().position, "unexpected token after expression",
		)
	}
	if root.resultKind() != expected {
		return nil, nil, sourceError(
			ErrResultKind,
			0,
			"expression returns a different kind",
		)
	}
	references := make(map[string]valueKind)
	root.references(references)
	return root, references, nil
}

func (state *parser) parseTernary(nesting int) (expressionNode, error) {
	if nesting > maxNestingDepth {
		return nil, sourceError(
			ErrLimit, state.current().position, "nesting depth limit exceeded",
		)
	}
	condition, err := state.parseComparison(nesting)
	if err != nil || state.current().kind != tokenQuestion {
		return condition, err
	}
	position := state.current().position
	state.index++
	truthy, err := state.parseTernary(nesting + 1)
	if err != nil {
		return nil, err
	}
	if state.current().kind != tokenColon {
		return nil, sourceError(
			ErrSyntax,
			state.current().position,
			"ternary is missing colon",
		)
	}
	state.index++
	falsy, err := state.parseTernary(nesting + 1)
	if err != nil {
		return nil, err
	}
	if condition.resultKind() != predicateKind || truthy.resultKind() != decimalKind ||
		falsy.resultKind() != decimalKind {
		return nil, sourceError(
			ErrResultKind,
			position,
			"ternary requires a predicate and decimal branches",
		)
	}
	if err := state.addNode(position); err != nil {
		return nil, err
	}
	return &ternaryNode{condition: condition, truthy: truthy, falsy: falsy}, nil
}

func (state *parser) parseComparison(nesting int) (expressionNode, error) {
	left, err := state.parseAddition(nesting)
	if err != nil {
		return nil, err
	}
	operator := state.current()
	if operator.kind != tokenEqual && operator.kind != tokenGreater {
		return left, nil
	}
	state.index++
	right, err := state.parseAddition(nesting)
	if err != nil {
		return nil, err
	}
	if operator.kind == tokenEqual &&
		(left.resultKind() != stringKind || right.resultKind() != stringKind) {
		return nil, sourceError(
			ErrResultKind,
			operator.position,
			"equality requires strings",
		)
	}
	if operator.kind == tokenGreater &&
		(left.resultKind() != decimalKind || right.resultKind() != decimalKind) {
		return nil, sourceError(
			ErrResultKind,
			operator.position,
			"comparison requires decimals",
		)
	}
	if err := state.addNode(operator.position); err != nil {
		return nil, err
	}
	return &binaryNode{
		operator: operator.kind, left: left, right: right, kind: predicateKind,
	}, nil
}

func (state *parser) parseAddition(nesting int) (expressionNode, error) {
	left, err := state.parseMultiplication(nesting)
	for err == nil && state.current().kind == tokenAdd {
		operator := state.current()
		state.index++
		var right expressionNode
		right, err = state.parseMultiplication(nesting)
		if err == nil {
			left, err = state.numericBinary(operator, left, right)
		}
	}
	return left, err
}

func (state *parser) parseMultiplication(nesting int) (expressionNode, error) {
	left, err := state.parsePrimary(nesting)
	for err == nil && state.current().kind == tokenMultiply {
		operator := state.current()
		state.index++
		var right expressionNode
		right, err = state.parsePrimary(nesting)
		if err == nil {
			left, err = state.numericBinary(operator, left, right)
		}
	}
	return left, err
}

func (state *parser) numericBinary(
	operator token,
	left, right expressionNode,
) (expressionNode, error) {
	if left.resultKind() != decimalKind || right.resultKind() != decimalKind {
		return nil, sourceError(
			ErrResultKind,
			operator.position,
			"arithmetic requires decimals",
		)
	}
	if err := state.addNode(operator.position); err != nil {
		return nil, err
	}
	return &binaryNode{
		operator: operator.kind, left: left, right: right, kind: decimalKind,
	}, nil
}

func (state *parser) parsePrimary(nesting int) (expressionNode, error) {
	current := state.current()
	state.index++
	if err := state.addNode(current.position); err != nil {
		return nil, err
	}

	switch current.kind {
	case tokenIdentifier:
		kind, ok := identifierKind(current.text)
		if !ok {
			return nil, sourceError(
				ErrUnsupported,
				current.position,
				"identifier is not allowlisted",
			)
		}
		return &identifierNode{name: current.text, kind: kind}, nil
	case tokenDecimal:
		decimal, err := ParseDecimal(current.text)
		if err != nil {
			return nil, sourceError(
				ErrSyntax,
				current.position,
				"decimal literal is invalid",
			)
		}
		return &literalNode{value: runtimeValue{kind: decimalKind, decimal: decimal}}, nil
	case tokenString:
		return &literalNode{value: runtimeValue{kind: stringKind, text: current.text}}, nil
	case tokenLeftParen:
		state.nodes--
		inner, err := state.parseTernary(nesting + 1)
		if err != nil {
			return nil, err
		}
		if state.current().kind != tokenRightParen {
			return nil, sourceError(
				ErrSyntax,
				state.current().position,
				"parenthesized expression is not closed",
			)
		}
		state.index++
		return inner, nil
	default:
		return nil, sourceError(ErrSyntax, current.position, "expected a value")
	}
}

func (state *parser) current() token {
	return state.tokens[state.index]
}

func (state *parser) addNode(position int) error {
	state.nodes++
	if state.nodes > maxNodes {
		return sourceError(ErrLimit, position, "semantic node limit exceeded")
	}
	return nil
}

func useStep(steps *int) error {
	(*steps)++
	if *steps > maxEvalSteps {
		return sourceError(ErrLimit, -1, "evaluation step limit exceeded")
	}
	return nil
}

func identifierKind(name string) (valueKind, bool) {
	switch name {
	case "AREA", "NUM_OF_ADULTS", "NUM_OF_CHILDREN", "NUM_OF_CLAIM":
		return decimalKind, true
	case "DESTINATION", "FLOOD", "TYP":
		return stringKind, true
	default:
		return 0, false
	}
}
