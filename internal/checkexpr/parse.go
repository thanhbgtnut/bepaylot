// Package checkexpr parses and evaluates the formulas of document checks
// (§6.9.9, U87): Excel-like expressions over the fields of the documents of
// one contract group, written as type.field with the codes of the KB, e.g.
//
//	SUM(hoa_don.tong_tien) <= hop_dong.gia_tri + SUM(phu_luc.gia_tri)
//	BETWEEN(hoa_don.ngay_lap, hop_dong.ngay_ky, hop_dong.ngay_het_han)
//
// A field of a type with several documents is a list; comparisons apply to
// each item and report the documents that fail. Logic has three values: a
// missing field gives "not enough data", never a pass.
package checkexpr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// node is one node of a parsed formula.
type node struct {
	op   string // num str bool ident member call neg not and or + - * / = != < <= > >=
	num  float64
	str  string // str value, ident name, call name, member field
	args []*node
	pos  int
}

type token struct {
	kind string // num str ident op ( ) , . eof
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var out []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case unicode.IsDigit(r):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.') {
				j++
			}
			out = append(out, token{"num", string(rs[i:j]), i})
			i = j
		case r == '"' || r == '\'' || r == '“' || r == '”':
			end := r
			if r == '“' {
				end = '”'
			}
			j := i + 1
			for j < len(rs) && rs[j] != end && !(end == '”' && rs[j] == '"') {
				j++
			}
			if j >= len(rs) {
				return nil, fmt.Errorf("thiếu dấu đóng ngoặc kép (vị trí %d)", i+1)
			}
			out = append(out, token{"str", string(rs[i+1 : j]), i})
			i = j + 1
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_' || rs[j] == '-') {
				j++
			}
			out = append(out, token{"ident", string(rs[i:j]), i})
			i = j
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch {
			case two == "<=" || two == ">=" || two == "!=" || two == "<>" || two == "==" || two == "&&" || two == "||":
				out = append(out, token{"op", two, i})
				i += 2
			case strings.ContainsRune("+-*/=<>!≤≥≠", r):
				t := string(r)
				switch r {
				case '≤':
					t = "<="
				case '≥':
					t = ">="
				case '≠':
					t = "!="
				}
				out = append(out, token{"op", t, i})
				i++
			case strings.ContainsRune("(),.", r):
				out = append(out, token{string(r), string(r), i})
				i++
			default:
				return nil, fmt.Errorf("ký tự %q không dùng được trong công thức (vị trí %d)", r, i+1)
			}
		}
	}
	return append(out, token{"eof", "", len(rs)}), nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) isWord(w string) bool {
	t := p.peek()
	return t.kind == "ident" && strings.EqualFold(t.text, w)
}

// Parse reads a formula; a leading "=" is allowed.
func Parse(src string) (*node, error) {
	src = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "="))
	if src == "" {
		return nil, fmt.Errorf("công thức trống")
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != "eof" {
		return nil, fmt.Errorf("thừa %q ở vị trí %d", t.text, t.pos+1)
	}
	return n, nil
}

func (p *parser) or() (*node, error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.isWord("OR") || p.isWord("HOẶC") || (p.peek().kind == "op" && p.peek().text == "||") {
		t := p.next()
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = &node{op: "or", args: []*node{l, r}, pos: t.pos}
	}
	return l, nil
}

func (p *parser) and() (*node, error) {
	l, err := p.not()
	if err != nil {
		return nil, err
	}
	for p.isWord("AND") || p.isWord("VÀ") || (p.peek().kind == "op" && p.peek().text == "&&") {
		t := p.next()
		r, err := p.not()
		if err != nil {
			return nil, err
		}
		l = &node{op: "and", args: []*node{l, r}, pos: t.pos}
	}
	return l, nil
}

func (p *parser) not() (*node, error) {
	if p.isWord("NOT") || p.isWord("KHÔNG") || (p.peek().kind == "op" && p.peek().text == "!") {
		t := p.next()
		x, err := p.not()
		if err != nil {
			return nil, err
		}
		return &node{op: "not", args: []*node{x}, pos: t.pos}, nil
	}
	return p.cmp()
}

func (p *parser) cmp() (*node, error) {
	l, err := p.add()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind == "op" {
		op := t.text
		switch op {
		case "==":
			op = "="
		case "<>":
			op = "!="
		}
		if op == "=" || op == "!=" || op == "<" || op == "<=" || op == ">" || op == ">=" {
			p.next()
			r, err := p.add()
			if err != nil {
				return nil, err
			}
			return &node{op: op, args: []*node{l, r}, pos: t.pos}, nil
		}
	}
	return l, nil
}

func (p *parser) add() (*node, error) {
	l, err := p.mul()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t.kind == "op" && (t.text == "+" || t.text == "-"); t = p.peek() {
		p.next()
		r, err := p.mul()
		if err != nil {
			return nil, err
		}
		l = &node{op: t.text, args: []*node{l, r}, pos: t.pos}
	}
	return l, nil
}

func (p *parser) mul() (*node, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t.kind == "op" && (t.text == "*" || t.text == "/"); t = p.peek() {
		p.next()
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		l = &node{op: t.text, args: []*node{l, r}, pos: t.pos}
	}
	return l, nil
}

func (p *parser) unary() (*node, error) {
	if t := p.peek(); t.kind == "op" && t.text == "-" {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &node{op: "neg", args: []*node{x}, pos: t.pos}, nil
	}
	return p.primary()
}

func (p *parser) primary() (*node, error) {
	t := p.next()
	switch t.kind {
	case "num":
		x, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("số %q không hợp lệ", t.text)
		}
		return &node{op: "num", num: x, pos: t.pos}, nil
	case "str":
		return &node{op: "str", str: t.text, pos: t.pos}, nil
	case "(":
		n, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.next().kind != ")" {
			return nil, fmt.Errorf("thiếu dấu đóng ngoặc")
		}
		return n, nil
	case "ident":
		if strings.EqualFold(t.text, "TRUE") || strings.EqualFold(t.text, "ĐÚNG") {
			return &node{op: "bool", num: 1, pos: t.pos}, nil
		}
		if strings.EqualFold(t.text, "FALSE") || strings.EqualFold(t.text, "SAI") {
			return &node{op: "bool", pos: t.pos}, nil
		}
		if p.peek().kind == "(" {
			p.next()
			call := &node{op: "call", str: strings.ToUpper(t.text), pos: t.pos}
			if p.peek().kind != ")" {
				for {
					a, err := p.or()
					if err != nil {
						return nil, err
					}
					call.args = append(call.args, a)
					if p.peek().kind == "," {
						p.next()
						continue
					}
					break
				}
			}
			if p.next().kind != ")" {
				return nil, fmt.Errorf("thiếu dấu đóng ngoặc của %s", call.str)
			}
			return call, nil
		}
		id := &node{op: "ident", str: t.text, pos: t.pos}
		if p.peek().kind == "." {
			p.next()
			f := p.next()
			if f.kind != "ident" && f.kind != "num" {
				return nil, fmt.Errorf("sau %q. phải là key của trường", t.text)
			}
			return &node{op: "member", str: f.text, args: []*node{id}, pos: t.pos}, nil
		}
		return id, nil
	case "eof":
		return nil, fmt.Errorf("công thức dừng giữa chừng")
	}
	return nil, fmt.Errorf("không hiểu %q ở vị trí %d", t.text, t.pos+1)
}
