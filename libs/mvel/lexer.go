package mvel

import (
	"errors"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tokenIdentifier tokenKind = iota + 1
	tokenDecimal
	tokenString
	tokenAdd
	tokenMultiply
	tokenEqual
	tokenGreater
	tokenQuestion
	tokenColon
	tokenLeftParen
	tokenRightParen
	tokenEOF
)

type token struct {
	kind     tokenKind
	text     string
	position int
}

func tokenize(source string) ([]token, error) {
	if len(source) == 0 {
		return nil, sourceError(ErrSyntax, 0, "expression is empty")
	}
	if len(source) > maxSourceBytes {
		return nil, sourceError(ErrLimit, maxSourceBytes, "source byte limit exceeded")
	}
	if !utf8.ValidString(source) {
		return nil, sourceError(ErrSyntax, 0, "expression is not valid UTF-8")
	}

	tokens := make([]token, 0, 16)
	parenthesisDepth := 0
	for offset := 0; offset < len(source); {
		if isSpace(source[offset]) {
			offset++
			continue
		}
		if len(tokens) >= maxTokens {
			return nil, sourceError(ErrLimit, offset, "token limit exceeded")
		}

		start := offset
		switch current := source[offset]; {
		case isIdentifierStart(current):
			offset++
			for offset < len(source) && isIdentifierPart(source[offset]) {
				offset++
			}
			if offset-start > maxNameBytes {
				return nil, sourceError(
					ErrLimit,
					start,
					"identifier byte limit exceeded",
				)
			}
			tokens = append(tokens, token{
				kind: tokenIdentifier, text: source[start:offset], position: start,
			})
		case isDigit(current):
			offset++
			periodSeen := false
			for offset < len(source) {
				if source[offset] == '.' && !periodSeen {
					periodSeen = true
					offset++
					continue
				}
				if !isDigit(source[offset]) {
					break
				}
				offset++
			}
			literal := source[start:offset]
			if offset < len(source) && source[offset] == 'B' {
				offset++
			}
			parsed, err := ParseDecimal(literal)
			if err != nil {
				category := ErrSyntax
				if errors.Is(err, ErrLimit) {
					category = ErrLimit
				}
				return nil, sourceError(
					category,
					start,
					"decimal literal is invalid",
				)
			}
			tokens = append(tokens, token{
				kind: tokenDecimal, text: parsed.String(), position: start,
			})
		case current == '\'':
			offset++
			contentStart := offset
			for offset < len(source) && source[offset] != '\'' {
				offset++
			}
			if offset == len(source) {
				return nil, sourceError(
					ErrSyntax,
					start,
					"string is not terminated",
				)
			}
			if offset-contentStart > maxStringBytes {
				return nil, sourceError(
					ErrLimit,
					start,
					"string byte limit exceeded",
				)
			}
			tokens = append(tokens, token{
				kind:     tokenString,
				text:     source[contentStart:offset],
				position: start,
			})
			offset++
		case current == '+':
			tokens = append(tokens, token{kind: tokenAdd, text: "+", position: start})
			offset++
		case current == '*':
			tokens = append(
				tokens,
				token{kind: tokenMultiply, text: "*", position: start},
			)
			offset++
		case current == '>':
			tokens = append(
				tokens,
				token{kind: tokenGreater, text: ">", position: start},
			)
			offset++
		case current == '=' && offset+1 < len(source) && source[offset+1] == '=':
			tokens = append(
				tokens,
				token{kind: tokenEqual, text: "==", position: start},
			)
			offset += 2
		case current == '?':
			tokens = append(
				tokens,
				token{kind: tokenQuestion, text: "?", position: start},
			)
			offset++
		case current == ':':
			tokens = append(tokens, token{kind: tokenColon, text: ":", position: start})
			offset++
		case current == '(':
			parenthesisDepth++
			if parenthesisDepth > maxNestingDepth {
				return nil, sourceError(
					ErrLimit,
					start,
					"nesting depth limit exceeded",
				)
			}
			tokens = append(
				tokens,
				token{kind: tokenLeftParen, text: "(", position: start},
			)
			offset++
		case current == ')':
			parenthesisDepth--
			if parenthesisDepth < 0 {
				return nil, sourceError(
					ErrSyntax,
					start,
					"closing parenthesis has no match",
				)
			}
			tokens = append(
				tokens,
				token{kind: tokenRightParen, text: ")", position: start},
			)
			offset++
		default:
			return nil, sourceError(
				ErrUnsupported,
				start,
				"character is outside the grammar",
			)
		}
	}
	if parenthesisDepth != 0 {
		return nil, sourceError(ErrSyntax, len(source), "parenthesis is not closed")
	}
	return append(tokens, token{kind: tokenEOF, position: len(source)}), nil
}

func isSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r'
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isIdentifierStart(character byte) bool {
	return character >= 'A' && character <= 'Z'
}

func isIdentifierPart(character byte) bool {
	return isIdentifierStart(character) || isDigit(character) || character == '_'
}
