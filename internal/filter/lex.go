// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tokenKind is a lexical token.
type tokenKind int

const (
	tokenInvalidInput      tokenKind = iota
	tokenEOF                         // end of tokens
	tokenWSC                         // whitespace or comment
	tokenDot                         // .
	tokenHas                         // :
	tokenOr                          // OR
	tokenAnd                         // AND
	tokenNot                         // NOT
	tokenLparen                      // (
	tokenRparen                      // )
	tokenComma                       // ,
	tokenLessThan                    // <
	tokenGreaterThan                 // >
	tokenGreaterThanEquals           // >=
	tokenLessThanEquals              // <=
	tokenNotEquals                   // !=
	tokenMatchesRegexp               // =~
	tokenNotMatchesRegexp            // !~
	tokenEquals                      // =
	tokenMinus                       // -
	tokenPlus                        // +
	tokenTilde                       // ~
	tokenBackslash                   // \
	tokenString
	tokenText
)

var tokenKindStrings = [...]string{
	tokenInvalidInput:      "invalid",
	tokenEOF:               "EOF",
	tokenWSC:               "whitespace",
	tokenDot:               ".",
	tokenHas:               ":",
	tokenOr:                "OR",
	tokenAnd:               "AND",
	tokenNot:               "NOT",
	tokenLparen:            "(",
	tokenRparen:            ")",
	tokenComma:             ",",
	tokenLessThan:          "<",
	tokenGreaterThan:       ">",
	tokenGreaterThanEquals: ">=",
	tokenLessThanEquals:    "<=",
	tokenNotEquals:         "!=",
	tokenMatchesRegexp:     "=~",
	tokenNotMatchesRegexp:  "!~",
	tokenEquals:            "=",
	tokenMinus:             "-",
	tokenPlus:              "+",
	tokenTilde:             "~",
	tokenBackslash:         `\`,
	tokenString:            "string",
	tokenText:              "text",
}

// String returns the string representation of a tokenKind.
func (tk tokenKind) String() string {
	return tokenKindStrings[tk]
}

// token is a lexical token read from the filter string.
type token struct {
	kind tokenKind
	pos  position
	val  string // only set for tokenString and tokenText
}

// position describes a position in the filter string.
type position struct {
	line, col int
}

// String prints a position for an error message.
func (pos position) String() string {
	return fmt.Sprintf("%d:%d", pos.line, pos.col)
}

// lexer is used to convert a string into a sequence of tokens.
// The exact separation into names and other tokens,
// and the handling of escape sequences,
// is pretty loosely defined.
type lexer struct {
	input string

	pushed      bool
	pushedToken token // valid if pushed

	prevDot  bool // previous token was tokenDot
	prevText bool // previous token was tokenText

	nextPos position // position of next token
}

// next returns the next token from the string.
// At the end of the input this returns tokenEOF.
func (lex *lexer) nextToken() token {
	if lex.pushed {
		lex.pushed = false
		return lex.pushedToken
	}

	pos := lex.nextPos

	prevDot := lex.prevDot
	prevText := lex.prevText
	lex.prevDot = false
	lex.prevText = false

	if len(lex.input) == 0 {
		return token{kind: tokenEOF, pos: pos}
	}

	r, size := utf8.DecodeRuneInString(lex.input)
	lex.advance(r, size)
	if r == utf8.RuneError {
		return token{kind: tokenInvalidInput, pos: pos}
	}

	switch {
	case isWhite(r):
		lex.skipWhite()
		return token{kind: tokenWSC, pos: pos}
	case r == '-':
		if len(lex.input) == 0 {
			return token{kind: tokenMinus, pos: pos}
		}
		rn, size := utf8.DecodeRuneInString(lex.input)
		if rn == '-' {
			// Comment.
			lex.advance(rn, size)
			lex.skipComment()
			lex.skipWhite()
			return token{kind: tokenWSC, pos: pos}
		}
		if isDigit(rn) {
			return lex.text(r, prevDot)
		}
		if !prevText && rn == '.' && len(lex.input) > size && isDigit(rune(lex.input[size])) {
			return lex.text(r, prevDot)
		}
		return token{kind: tokenMinus, pos: pos}
	case r == '.':
		if len(lex.input) == 0 {
			lex.prevDot = true
			return token{kind: tokenDot, pos: pos}
		}
		if !prevText {
			rn, _ := utf8.DecodeRuneInString(lex.input)
			if isDigit(rn) {
				return lex.text(r, prevDot)
			}
		}
		lex.prevDot = true
		return token{kind: tokenDot, pos: pos}
	case r == ':':
		return token{kind: tokenHas, pos: pos}
	case r == 'O':
		if len(lex.input) > 0 && lex.input[0] == 'R' && (len(lex.input) < 2 || isWhite(rune(lex.input[1]))) {
			lex.advance('R', 1)
			return token{kind: tokenOr, pos: pos}
		}
		return lex.text(r, prevDot)
	case r == 'A':
		if len(lex.input) > 1 && lex.input[:2] == "ND" && (len(lex.input) < 3 || isWhite(rune(lex.input[2]))) {
			lex.advance('N', 1)
			lex.advance('D', 1)
			return token{kind: tokenAnd, pos: pos}
		}
		return lex.text(r, prevDot)
	case r == 'N':
		if len(lex.input) > 1 && lex.input[:2] == "OT" && (len(lex.input) < 3 || isWhite(rune(lex.input[2]))) {
			lex.advance('O', 1)
			lex.advance('T', 1)
			return token{kind: tokenNot, pos: pos}
		}
		return lex.text(r, prevDot)
	case r == '(':
		return token{kind: tokenLparen, pos: pos}
	case r == ')':
		return token{kind: tokenRparen, pos: pos}
	case r == ',':
		return token{kind: tokenComma, pos: pos}
	case r == '<':
		if len(lex.input) > 0 && lex.input[0] == '=' {
			lex.advance('=', 1)
			return token{kind: tokenLessThanEquals, pos: pos}
		}
		return token{kind: tokenLessThan, pos: pos}
	case r == '>':
		if len(lex.input) > 0 && lex.input[0] == '=' {
			lex.advance('=', 1)
			return token{kind: tokenGreaterThanEquals, pos: pos}
		}
		return token{kind: tokenGreaterThan, pos: pos}
	case r == '!':
		if len(lex.input) > 0 {
			if lex.input[0] == '=' {
				lex.advance('=', 1)
				return token{kind: tokenNotEquals, pos: pos}
			}
			if lex.input[0] == '~' {
				lex.advance('~', 1)
				return token{kind: tokenNotMatchesRegexp, pos: pos}
			}
		}
		// A bare ! is part of a name.
		return lex.text(r, prevDot)
	case r == '=':
		if len(lex.input) > 0 && lex.input[0] == '~' {
			lex.advance('~', 1)
			return token{kind: tokenMatchesRegexp, pos: pos}
		}
		return token{kind: tokenEquals, pos: pos}
	case r == '+':
		return token{kind: tokenPlus, pos: pos}
	case r == '~':
		return token{kind: tokenTilde, pos: pos}
	case r == '"':
		return lex.collectString()
	case isTextStart(r):
		return lex.text(r, prevDot)
	case isDigit(r):
		return lex.text(r, prevDot)

	default:
		return token{kind: tokenInvalidInput, pos: pos}
	}
}

// skipWhite skips whitespace.
func (lex *lexer) skipWhite() {
	for len(lex.input) > 0 {
		r, size := utf8.DecodeRuneInString(lex.input)
		switch {
		case isWhite(r):
			lex.advance(r, size)
		case r == '-':
			if len(lex.input) <= size {
				return
			}
			rn, sizen := utf8.DecodeRuneInString(lex.input[size:])
			if rn != '-' {
				return
			}
			lex.advance(r, size)
			lex.advance(rn, sizen)
			lex.skipComment()
		default:
			return
		}
	}
}

// skipComment skips a comment.
// We have already skipped the initial two '-' characters.
func (lex *lexer) skipComment() {
	for len(lex.input) > 0 {
		r, size := utf8.DecodeRuneInString(lex.input)
		switch {
		case r == '\t',
			r >= ' ' && r <= '~',
			isa1OrHigher(r):

			lex.advance(r, size)

		default:
			return
		}
	}
}

// advance advances past r of size size.
func (lex *lexer) advance(r rune, size int) {
	lex.input = lex.input[size:]
	if r == '\n' {
		lex.nextPos.line++
		lex.nextPos.col = 0
	} else {
		lex.nextPos.col++
	}
}

// text collects a name.
// r is the first rune in the name; we have already advanced past it.
// prevDot records whether the previous token was tokenDot;
// in that case we don't include a dot after a number prefix.
func (lex *lexer) text(r rune, prevDot bool) token {
	pos := lex.nextPos

	var sb strings.Builder

	if r != '\\' {
		sb.WriteRune(r)
	} else {
		if !lex.textEsc(&sb) {
			return token{kind: tokenInvalidInput, pos: pos}
		}
	}

	// Add a number prefix.
	if r == '-' || r == '.' || isDigit(r) {
		for len(lex.input) > 0 {
			rn, size := utf8.DecodeRuneInString(lex.input)
			if isDigit(rn) {
				sb.WriteRune(rn)
				lex.advance(rn, size)
				if r == '.' {
					break
				}
			} else if !prevDot && r != '.' && rn == '.' {
				sb.WriteRune(rn)
				lex.advance(rn, size)
				break
			} else {
				break
			}
			if r == '-' {
				r = rn
			}
		}
	}

	for len(lex.input) > 0 {
		rn, size := utf8.DecodeRuneInString(lex.input)
		if rn == '\\' {
			lex.advance(rn, size)
			if !lex.textEsc(&sb) {
				return token{kind: tokenInvalidInput, pos: pos}
			}
		} else if isTextStart(rn) || isDigit(rn) || rn == '+' || rn == '-' {
			sb.WriteRune(rn)
			lex.advance(rn, size)
		} else if rn == '!' {
			// A bare '!' is part of a name for some reason,
			// but not != or !~.
			if len(lex.input) > 1 && (lex.input[1] == '=' || lex.input[1] == '~') {
				break
			}
			sb.WriteRune(rn)
			lex.advance(rn, size)
		} else {
			break
		}
	}

	lex.prevText = true

	return token{kind: tokenText, pos: pos, val: sb.String()}
}

// quotedSpecials is a map from a character that can follow a
// backslash to the byte value that it represents.
var quotedSpecials = map[rune]byte{
	'a': '\a',
	'b': '\b',
	'f': '\f',
	'n': '\n',
	'r': '\r',
	't': '\t',
	'v': '\v',
}

// collectString collects the characters from a string.
func (lex *lexer) collectString() token {
	pos := lex.nextPos

	check := false
	var sb strings.Builder
	for len(lex.input) > 0 {
		r, size := utf8.DecodeRuneInString(lex.input)
		lex.advance(r, size)
		switch {
		case isWhite(r),
			r == '!',
			r >= '#' && r <= '[', // does not include '\\'
			r >= ']' && r <= '~',
			isa1OrHigher(r):

			sb.WriteRune(r)

		case r == '"':
			s := sb.String()
			if check && !utf8.ValidString(s) {
				return token{kind: tokenInvalidInput, pos: pos}
			}
			return token{kind: tokenString, pos: pos, val: s}

		case r == '\\':
			check = true
			if len(lex.input) == 0 {
				return token{kind: tokenInvalidInput, pos: pos}
			}
			r, size = utf8.DecodeRuneInString(lex.input)
			if !lex.handleTextEsc(r, &sb) {
				if s, ok := quotedSpecials[r]; ok {
					lex.advance(r, size)
					sb.WriteByte(s)
				} else {
					// Don't advance past r,
					// just treat it as the next rune.
					sb.WriteByte('\\')
				}
			}
		}
	}
	return token{kind: tokenInvalidInput, pos: pos}
}

// textEsc is called with the input pointing after a backslash.
// If we see an escape sequence that can appear in a string or a name,
// this adds a rune to sb and advances input.
// It reports whether it added a rune.
func (lex *lexer) textEsc(sb *strings.Builder) bool {
	if len(lex.input) == 0 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(lex.input)
	return lex.handleTextEsc(r, sb)
}

// handleTextEsc is called with a character following a backslash.
// The character is still in lex.input.
// If this is an escape sequence that can appear in a string or a name,
// this adds a rune to sb and advances input.
// It reports whether it added a rune.
func (lex *lexer) handleTextEsc(r rune, sb *strings.Builder) bool {
	switch r {
	case ',', ':', '=', '<', '>', '+', '~', '"', '\\', '.', '*':
		lex.advance(r, 1)
		sb.WriteRune(r)
		return true

	case 'u':
		return lex.handleHex(r, sb)

	case '0', '1', '2', '3', '4', '5', '6', '7':
		// A backslash sequence of 1-3 octal digits is an octal escape.
		if len(lex.input) >= 3 && r <= '3' && isOctalDigit(rune(lex.input[1])) && isOctalDigit(rune(lex.input[2])) {
			lex.advance(r, 1)
			r -= '0'
			r <<= 6
			r += rune(lex.input[1]-'0') << 3
			r += rune(lex.input[2] - '0')
			lex.advance(rune(lex.input[1]), 1)
			lex.advance(rune(lex.input[2]), 1)
			sb.WriteRune(r)
			return true
		}
		if len(lex.input) >= 2 && isOctalDigit(rune(lex.input[1])) {
			lex.advance(r, 1)
			r -= '0'
			r <<= 3
			r += rune(lex.input[1] - '0')
			lex.advance(rune(lex.input[1]), 1)
			sb.WriteRune(r)
			return true
		}
		lex.advance(r, 1)
		r -= '0'
		sb.WriteRune(r)
		return true

	case 'x':
		return lex.handleHex(r, sb)

	default:
		return false
	}
}

// handleHex is a subroutine of handleTextEsc.
// r is the escape sequence starter, either 'u' for a \uDDDD escape
// or 'r' for a \xHH escape.
func (lex *lexer) handleHex(r rune, sb *strings.Builder) bool {
	var ln int
	if r == 'u' {
		ln = 4
	} else {
		ln = 2
	}

	if len(lex.input) < ln+1 {
		return false
	}

	for i := range ln {
		if !isHexDigit(rune(lex.input[i+1])) {
			return false
		}
	}

	// Skip the 'u' or 'h' starter.
	lex.advance(r, 1)

	r = 0
	for range ln {
		r1 := rune(lex.input[0])
		lex.advance(r1, 1)
		r <<= 4
		if r1 >= 'a' && r1 <= 'f' {
			r += 10 + r1 - 'a'
		} else if r1 >= 'A' && r1 <= 'F' {
			r += 10 + r1 - 'A'
		} else {
			r += r1 - '0'
		}
	}
	if r == 'u' {
		// \uDDDD is the rune DDDD.
		sb.WriteRune(r)
	} else {
		// /xHH is the byte HH.
		sb.WriteByte(byte(r))
	}
	return true
}

// pushToken pushes a token so that it is the next one returned.
func (lex *lexer) pushToken(tok token) {
	if lex.pushed {
		panic("double pushToken")
	}
	lex.pushed = true
	lex.pushedToken = tok
}

// isWhite reports whether r is a whitespace character.
// These are the whitespace characters recognized by the C++ lexer.
func isWhite(r rune) bool {
	switch r {
	case ' ', '\t', '\f', '\u00a0', '\r', '\n':
		return true
	default:
		return false
	}
}

// isDigit reports whether r is a digit.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isOctalDigit reports whether r is an octal digit.
func isOctalDigit(r rune) bool {
	return r >= '0' && r <= '7'
}

// isHexDigit reports whether r is a hex digit.
func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// isTextStart reports whether r can be start of a name.
func isTextStart(r rune) bool {
	switch {
	case r >= '#' && r <= '\'':
	case r == '*':
	case r == '/':
	case r == ';':
	case r == '?':
	case r == '@':
	case r >= 'A' && r <= 'Z':
	case r == '[':
	case r == ']':
	case r >= '^' && r <= '}': // includes lower-case ASCII letters
	case isa1OrHigher(r):
	case r == '\\':

	default:
		return false
	}

	return true
}

// isa1OrHigher reports whether r is a rune larger than \u00a1.
func isa1OrHigher(r rune) bool {
	return r >= '\u00a1' && r <= '\U000effff'
}
