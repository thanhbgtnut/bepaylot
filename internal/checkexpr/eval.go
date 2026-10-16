package checkexpr

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thanhenti/bepaylot/docpipe/textutil"
)

// Doc is one document in scope: its type, a label for messages and its
// fields by key (the current value text and the field id, for evidence).
type Doc struct {
	ID     string                `json:"id"`
	Type   string                `json:"type"`
	Label  string                `json:"label"`
	Fields map[string]FieldValue `json:"fields"`
}

// FieldValue is the value of one field of a document.
type FieldValue struct {
	Text    string `json:"text"`
	FieldID string `json:"field_id,omitempty"`
}

// TypeInfo describes a document type for checking a formula: its title and
// the value type of each field key.
type TypeInfo struct {
	Title  string
	Fields map[string]FieldInfo
}

// FieldInfo is one field of a type.
type FieldInfo struct {
	Label     string
	ValueType string // string number money date bool
}

// Scope is what a formula sees: the KB types, the documents of each type in
// the group and the names bound to one document (the contract of the group,
// or the document being linked).
type Scope struct {
	Types  map[string]TypeInfo
	Groups map[string][]Doc
	Single map[string]Doc
	// SingleType gives the type of each single name (for Check).
	SingleType map[string]string
}

// Ref is a document (and field) a value comes from.
type Ref struct {
	Doc     string `json:"doc"`
	Label   string `json:"label"`
	FieldID string `json:"field_id,omitempty"`
}

type kind int

const (
	kUnknown kind = iota
	kNum
	kStr
	kDate
	kBool
	kList
)

// value is a value during evaluation.
type value struct {
	k       kind
	n       float64
	s       string
	t       time.Time
	b       bool
	l       []value
	refs    []Ref
	missing []string // why it is unknown
	note    string   // for a boolean: what was compared
	whole   bool     // computed from several documents: no document label in notes
}

func unknown(why ...string) value { return value{k: kUnknown, missing: why} }

func (v value) text() string {
	switch v.k {
	case kNum:
		return fmtNum(v.n)
	case kStr:
		return "“" + v.s + "”"
	case kDate:
		return v.t.Format("02/01/2006")
	case kBool:
		if v.b {
			return "đúng"
		}
		return "sai"
	case kList:
		parts := make([]string, 0, len(v.l))
		for _, x := range v.l {
			parts = append(parts, x.text())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return "(không có)"
}

func fmtNum(x float64) string {
	if x == math.Trunc(x) && math.Abs(x) < 1e15 {
		s := strconv.FormatInt(int64(math.Abs(x)), 10)
		var b strings.Builder
		for i, r := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				b.WriteByte('.')
			}
			b.WriteRune(r)
		}
		if x < 0 {
			return "-" + b.String()
		}
		return b.String()
	}
	return strconv.FormatFloat(x, 'f', 2, 64)
}

// Check validates a formula against the scope's types: known names, keys,
// functions and argument counts. It returns the (type, key) pairs it reads.
func Check(src string, sc Scope) ([][2]string, error) {
	n, err := Parse(src)
	if err != nil {
		return nil, err
	}
	var used [][2]string
	var walk func(*node) error
	walk = func(n *node) error {
		switch n.op {
		case "ident":
			if _, ok := sc.typeOf(n.str); !ok {
				return fmt.Errorf("không có loại giấy tờ %q", n.str)
			}
		case "member":
			t, ok := sc.typeOf(n.args[0].str)
			if !ok {
				return fmt.Errorf("không có loại giấy tờ %q", n.args[0].str)
			}
			if _, ok := sc.Types[t].Fields[n.str]; !ok {
				return fmt.Errorf("loại %s không có trường %q", sc.Types[t].Title, n.str)
			}
			used = append(used, [2]string{t, n.str})
			return nil
		case "call":
			f, ok := funcs[n.str]
			if !ok {
				return fmt.Errorf("không có hàm %s", n.str)
			}
			if len(n.args) < f.min || (f.max >= 0 && len(n.args) > f.max) {
				return fmt.Errorf("hàm %s cần %s", n.str, f.arity)
			}
		}
		for _, a := range n.args {
			if err := walk(a); err != nil {
				return err
			}
		}
		return nil
	}
	return used, walk(n)
}

func (sc Scope) typeOf(name string) (string, bool) {
	if t, ok := sc.SingleType[name]; ok {
		return t, true
	}
	if _, ok := sc.Types[name]; ok {
		return name, true
	}
	return "", false
}

// Status of a check.
const (
	Pass    = "pass"
	Fail    = "fail"
	Unknown = "unknown"
	Error   = "error"
)

// Result is the outcome of a formula.
type Result struct {
	Status  string   `json:"status"`
	Detail  string   `json:"detail,omitempty"`  // what was compared, for a pass or a fail
	Failing []Item   `json:"failing,omitempty"` // the items that fail
	Missing []string `json:"missing,omitempty"` // what is missing, for unknown
	Refs    []Ref    `json:"refs,omitempty"`    // documents the result reads
}

// Item is one failing item of a list check.
type Item struct {
	Note string `json:"note"`
	Refs []Ref  `json:"refs"`
}

// Eval evaluates a formula that gives yes/no.
func Eval(src string, sc Scope) Result {
	n, err := Parse(src)
	if err != nil {
		return Result{Status: Error, Detail: err.Error()}
	}
	v, err := (&evaluator{sc: sc}).eval(n)
	if err != nil {
		return Result{Status: Error, Detail: err.Error()}
	}
	return verdict(v)
}

func verdict(v value) Result {
	switch v.k {
	case kBool:
		r := Result{Status: Pass, Detail: v.note, Refs: v.refs}
		if !v.b {
			r.Status = Fail
			r.Failing = []Item{{Note: v.note, Refs: v.refs}}
		}
		return r
	case kList:
		if len(v.l) == 0 {
			return Result{Status: Unknown, Missing: []string{"không có giấy tờ nào để kiểm"}}
		}
		r := Result{Status: Pass}
		var missing []string
		for _, x := range v.l {
			sub := verdict(x)
			r.Refs = append(r.Refs, sub.Refs...)
			switch sub.Status {
			case Fail:
				r.Status = Fail
				r.Failing = append(r.Failing, sub.Failing...)
			case Unknown:
				missing = append(missing, sub.Missing...)
			case Error:
				return sub
			}
		}
		if r.Status == Pass && len(missing) > 0 {
			return Result{Status: Unknown, Missing: dedupe(missing), Refs: r.Refs}
		}
		if r.Status == Pass {
			r.Detail = fmt.Sprintf("%d/%d giấy tờ đạt", len(v.l), len(v.l))
		}
		return r
	case kUnknown:
		return Result{Status: Unknown, Missing: dedupe(v.missing), Refs: v.refs}
	}
	return Result{Status: Error, Detail: "công thức phải cho kết quả đúng/sai, nhưng cho " + v.text()}
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

type evaluator struct{ sc Scope }

func (e *evaluator) field(d Doc, typ, key string) value {
	fi := e.sc.Types[typ].Fields[key]
	fv := d.Fields[key]
	ref := Ref{Doc: d.ID, Label: d.Label, FieldID: fv.FieldID}
	label := fi.Label
	if label == "" {
		label = key
	}
	t := strings.TrimSpace(fv.Text)
	if t == "" {
		v := unknown(d.Label + ": thiếu " + label)
		v.refs = []Ref{{Doc: d.ID, Label: d.Label}}
		return v
	}
	v := value{k: kStr, s: t, refs: []Ref{ref}}
	switch fi.ValueType {
	case "money", "number":
		if x, ok := textutil.ParseNumber(t); ok {
			v = value{k: kNum, n: x, refs: v.refs}
		} else {
			v = unknown(fmt.Sprintf("%s: %s %q không đọc được thành số", d.Label, label, t))
			v.refs = []Ref{ref}
		}
	case "date":
		if d2, ok := parseDate(t); ok {
			v = value{k: kDate, t: d2, refs: v.refs}
		} else {
			v = unknown(fmt.Sprintf("%s: %s %q không đọc được thành ngày", d.Label, label, t))
			v.refs = []Ref{ref}
		}
	case "bool":
		switch textutil.Normalize(t) {
		case "co", "yes", "true", "dung", "x", "v":
			v = value{k: kBool, b: true, refs: v.refs}
		case "khong", "no", "false", "sai":
			v = value{k: kBool, refs: v.refs}
		}
	}
	return v
}

var dateLayouts = []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "02.01.2006", "2006/01/02"}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (e *evaluator) eval(n *node) (value, error) {
	switch n.op {
	case "num":
		return value{k: kNum, n: n.num}, nil
	case "str":
		if t, ok := parseDate(n.str); ok {
			return value{k: kDate, t: t, s: n.str}, nil
		}
		return value{k: kStr, s: n.str}, nil
	case "bool":
		return value{k: kBool, b: n.num != 0}, nil
	case "ident":
		if d, ok := e.sc.Single[n.str]; ok {
			return value{k: kStr, s: d.Label, refs: []Ref{{Doc: d.ID, Label: d.Label}}}, nil
		}
		docs := e.sc.Groups[n.str]
		l := make([]value, 0, len(docs))
		for _, d := range docs {
			l = append(l, value{k: kStr, s: d.Label, refs: []Ref{{Doc: d.ID, Label: d.Label}}})
		}
		return value{k: kList, l: l}, nil
	case "member":
		name := n.args[0].str
		typ, _ := e.sc.typeOf(name)
		if d, ok := e.sc.Single[name]; ok {
			return e.field(d, typ, n.str), nil
		}
		if _, single := e.sc.SingleType[name]; single {
			return unknown("chưa có " + e.sc.Types[typ].Title), nil
		}
		docs := e.sc.Groups[name]
		l := make([]value, 0, len(docs))
		for _, d := range docs {
			l = append(l, e.field(d, typ, n.str))
		}
		return value{k: kList, l: l}, nil
	case "neg":
		x, err := e.eval(n.args[0])
		if err != nil {
			return x, err
		}
		return arith("-", value{k: kNum}, x)
	case "not":
		x, err := e.eval(n.args[0])
		if err != nil {
			return x, err
		}
		return mapBool(x, func(b value) value { b.b = !b.b; b.note = "không phải: " + b.note; return b }), nil
	case "and", "or":
		l, err := e.eval(n.args[0])
		if err != nil {
			return l, err
		}
		r, err := e.eval(n.args[1])
		if err != nil {
			return r, err
		}
		return logic(n.op, l, r), nil
	case "+", "-", "*", "/":
		l, err := e.eval(n.args[0])
		if err != nil {
			return l, err
		}
		r, err := e.eval(n.args[1])
		if err != nil {
			return r, err
		}
		return arith(n.op, l, r)
	case "=", "!=", "<", "<=", ">", ">=":
		l, err := e.eval(n.args[0])
		if err != nil {
			return l, err
		}
		r, err := e.eval(n.args[1])
		if err != nil {
			return r, err
		}
		return broadcast2(l, r, func(a, b value) value { return compare(n.op, a, b) }), nil
	case "call":
		args := make([]value, 0, len(n.args))
		for _, a := range n.args {
			v, err := e.eval(a)
			if err != nil {
				return v, err
			}
			args = append(args, v)
		}
		return funcs[n.str].fn(args)
	}
	return value{}, fmt.Errorf("không hiểu phép %s", n.op)
}

// logic combines two yes/no values with three-valued logic: false AND
// unknown is false, true OR unknown is true.
func logic(op string, l, r value) value {
	lv, rv := verdict(l), verdict(r)
	both := append(append([]Ref(nil), l.refs...), r.refs...)
	toBool := func(res Result, src value) (bool, bool) { // value, known
		switch res.Status {
		case Pass:
			return true, true
		case Fail:
			return false, true
		}
		return false, false
	}
	lb, lk := toBool(lv, l)
	rb, rk := toBool(rv, r)
	notes := func() string {
		var parts []string
		for _, res := range []Result{lv, rv} {
			if res.Detail != "" {
				parts = append(parts, res.Detail)
			}
			for _, f := range res.Failing {
				parts = append(parts, f.Note)
			}
		}
		return strings.Join(dedupe(parts), "; ")
	}
	if op == "and" {
		if (lk && !lb) || (rk && !rb) {
			out := value{k: kBool, b: false, refs: both}
			var parts []string
			for _, res := range []Result{lv, rv} {
				for _, f := range res.Failing {
					parts = append(parts, f.Note)
				}
			}
			out.note = strings.Join(dedupe(parts), "; ")
			return out
		}
		if lk && rk {
			return value{k: kBool, b: true, refs: both, note: notes()}
		}
	} else {
		if (lk && lb) || (rk && rb) {
			return value{k: kBool, b: true, refs: both, note: notes()}
		}
		if lk && rk {
			return value{k: kBool, b: false, refs: both, note: notes()}
		}
	}
	return unknown(append(lv.Missing, rv.Missing...)...)
}

func mapBool(v value, f func(value) value) value {
	switch v.k {
	case kBool:
		return f(v)
	case kList:
		out := v
		out.l = make([]value, len(v.l))
		for i, x := range v.l {
			out.l[i] = mapBool(x, f)
		}
		return out
	}
	return v
}

// broadcast2 applies f item by item when one side is a list (the other is
// compared with each item); two lists of the same length go pairwise.
func broadcast2(l, r value, f func(a, b value) value) value {
	switch {
	case l.k == kList && r.k == kList && len(l.l) == len(r.l):
		out := value{k: kList}
		for i := range l.l {
			out.l = append(out.l, broadcast2(l.l[i], r.l[i], f))
		}
		return out
	case l.k == kList && r.k != kList:
		out := value{k: kList}
		for _, x := range l.l {
			out.l = append(out.l, broadcast2(x, r, f))
		}
		return out
	case r.k == kList && l.k != kList:
		out := value{k: kList}
		for _, x := range r.l {
			out.l = append(out.l, broadcast2(l, x, f))
		}
		return out
	case l.k == kList && r.k == kList:
		return unknown("hai danh sách không cùng số giấy tờ, hãy gộp bằng SUM/MIN/MAX")
	}
	if l.k == kUnknown || r.k == kUnknown {
		v := unknown(append(l.missing, r.missing...)...)
		v.refs = append(append([]Ref(nil), l.refs...), r.refs...)
		return v
	}
	return f(l, r)
}

func refsOf(vs ...value) []Ref {
	var out []Ref
	for _, v := range vs {
		out = append(out, v.refs...)
	}
	return out
}

func compare(op string, a, b value) value {
	var c int
	switch {
	case a.k == kNum && b.k == kNum:
		switch {
		case math.Abs(a.n-b.n) < 0.005:
			c = 0
		case a.n < b.n:
			c = -1
		default:
			c = 1
		}
	case a.k == kDate && b.k == kDate:
		c = a.t.Compare(b.t)
	case a.k == kBool && b.k == kBool:
		if a.b != b.b {
			c = 1
		}
	default:
		ka, kb := textutil.ValueKey(asText(a)), textutil.ValueKey(asText(b))
		c = strings.Compare(ka, kb)
		if op != "=" && op != "!=" {
			return unknown(fmt.Sprintf("không so lớn nhỏ được %s với %s", a.text(), b.text()))
		}
	}
	ok := map[string]bool{"=": c == 0, "!=": c != 0, "<": c < 0, "<=": c <= 0, ">": c > 0, ">=": c >= 0}[op]
	label := ""
	if len(a.refs) == 1 && !a.whole {
		label = a.refs[0].Label + ": "
	} else if len(b.refs) == 1 && !b.whole && len(a.refs) == 0 {
		label = b.refs[0].Label + ": "
	}
	sym := map[string]string{"=": "=", "!=": "≠", "<": "<", "<=": "≤", ">": ">", ">=": "≥"}[op]
	note := fmt.Sprintf("%s%s %s %s", label, a.text(), sym, b.text())
	if !ok {
		note = fmt.Sprintf("%s%s không %s %s", label, a.text(), sym, b.text())
	}
	return value{k: kBool, b: ok, refs: refsOf(a, b), note: note}
}

func asText(v value) string {
	switch v.k {
	case kStr:
		return v.s
	case kDate:
		return v.t.Format("2006-01-02")
	}
	return v.text()
}

func arith(op string, l, r value) (value, error) {
	if l.k == kList || r.k == kList {
		return broadcast2(l, r, func(a, b value) value { v, _ := arith(op, a, b); return v }), nil
	}
	if l.k == kUnknown || r.k == kUnknown {
		return unknown(append(l.missing, r.missing...)...), nil
	}
	if l.k != kNum || r.k != kNum {
		return unknown(fmt.Sprintf("phép %s chỉ dùng cho số, nhận %s và %s", op, l.text(), r.text())), nil
	}
	v := value{k: kNum, refs: refsOf(l, r), whole: l.whole || r.whole || len(refsOf(l, r)) > 1}
	switch op {
	case "+":
		v.n = l.n + r.n
	case "-":
		v.n = l.n - r.n
	case "*":
		v.n = l.n * r.n
	case "/":
		if r.n == 0 {
			return unknown("chia cho 0"), nil
		}
		v.n = l.n / r.n
	}
	return v, nil
}

// flat lists the items of the arguments (lists are opened).
func flat(args []value) []value {
	var out []value
	for _, a := range args {
		if a.k == kList {
			out = append(out, flat(a.l)...)
		} else {
			out = append(out, a)
		}
	}
	return out
}

type fn struct {
	min, max int
	arity    string
	fn       func([]value) (value, error)
}

var funcs map[string]fn

func init() {
	agg := func(name string, f func([]float64) float64) fn {
		return fn{1, -1, "ít nhất 1 giá trị", func(args []value) (value, error) {
			items := flat(args)
			var xs []float64
			var missing []string
			for _, x := range items {
				switch x.k {
				case kNum:
					xs = append(xs, x.n)
				case kUnknown:
					missing = append(missing, x.missing...)
				default:
					missing = append(missing, fmt.Sprintf("%s cần số, nhận %s", name, x.text()))
				}
			}
			if len(missing) > 0 {
				return unknown(missing...), nil
			}
			if len(xs) == 0 {
				if name == "SUM" {
					return value{k: kNum}, nil
				}
				return unknown(name + ": không có giá trị nào"), nil
			}
			return value{k: kNum, n: f(xs), refs: refsOf(items...), whole: true}, nil
		}}
	}
	funcs = map[string]fn{
		"SUM": agg("SUM", func(xs []float64) float64 {
			s := 0.0
			for _, x := range xs {
				s += x
			}
			return s
		}),
		"MIN": agg("MIN", func(xs []float64) float64 { sort.Float64s(xs); return xs[0] }),
		"MAX": agg("MAX", func(xs []float64) float64 { sort.Float64s(xs); return xs[len(xs)-1] }),
		"AVERAGE": agg("AVERAGE", func(xs []float64) float64 {
			s := 0.0
			for _, x := range xs {
				s += x
			}
			return s / float64(len(xs))
		}),
		"COUNT": {1, -1, "ít nhất 1 giá trị", func(args []value) (value, error) {
			n := 0
			for _, x := range flat(args) {
				if x.k != kUnknown {
					n++
				}
			}
			return value{k: kNum, n: float64(n)}, nil
		}},
		"EXISTS": {1, 1, "1 giá trị", func(args []value) (value, error) {
			for _, x := range flat(args) {
				if x.k != kUnknown {
					return value{k: kBool, b: true, note: "có " + x.text()}, nil
				}
			}
			return value{k: kBool, note: "không có giấy tờ hoặc giá trị nào"}, nil
		}},
		"ABS": {1, 1, "1 số", func(a []value) (value, error) {
			if a[0].k != kNum {
				return unknown("ABS cần số"), nil
			}
			a[0].n = math.Abs(a[0].n)
			return a[0], nil
		}},
		"ROUND": {1, 2, "1 số và số chữ số", func(a []value) (value, error) {
			if a[0].k != kNum {
				return unknown("ROUND cần số"), nil
			}
			p := 0.0
			if len(a) > 1 && a[1].k == kNum {
				p = a[1].n
			}
			m := math.Pow(10, p)
			a[0].n = math.Round(a[0].n*m) / m
			return a[0], nil
		}},
		"BETWEEN": {3, 3, "giá trị, cận dưới, cận trên", func(a []value) (value, error) {
			return broadcast2(a[0], value{k: kList, l: []value{a[1], a[2]}}.pair(), func(x, lohi value) value {
				lo, hi := lohi.l[0], lohi.l[1]
				if lo.k == kUnknown || hi.k == kUnknown {
					return unknown(append(lo.missing, hi.missing...)...)
				}
				ge, le := compare(">=", x, lo), compare("<=", x, hi)
				if ge.k != kBool || le.k != kBool {
					return unknown(append(ge.missing, le.missing...)...)
				}
				label := ""
				if len(x.refs) > 0 {
					label = x.refs[0].Label + ": "
				}
				ok := ge.b && le.b
				note := fmt.Sprintf("%s%s trong khoảng %s – %s", label, x.text(), lo.text(), hi.text())
				if !ok {
					note = fmt.Sprintf("%s%s ngoài khoảng %s – %s", label, x.text(), lo.text(), hi.text())
				}
				return value{k: kBool, b: ok, refs: refsOf(x, lo, hi), note: note}
			}), nil
		}},
		"SAME": {1, -1, "các giá trị cần giống nhau", func(args []value) (value, error) {
			items := flat(args)
			byKey := map[string][]value{}
			var order []string
			var missing []string
			for _, x := range items {
				if x.k == kUnknown {
					missing = append(missing, x.missing...)
					continue
				}
				k := textutil.ValueKey(asText(x))
				if _, ok := byKey[k]; !ok {
					order = append(order, k)
				}
				byKey[k] = append(byKey[k], x)
			}
			known := len(items) - len(missing)
			if known < 2 {
				v := unknown(append(missing, "cần ít nhất 2 giá trị để so")...)
				return v, nil
			}
			if len(order) == 1 {
				return value{k: kBool, b: true, refs: refsOf(items...), note: fmt.Sprintf("%d giá trị giống nhau: %s", known, byKey[order[0]][0].text())}, nil
			}
			var parts []string
			for _, k := range order {
				var labels []string
				for _, x := range byKey[k] {
					for _, r := range x.refs {
						labels = append(labels, r.Label)
					}
				}
				parts = append(parts, fmt.Sprintf("%s (%s)", byKey[k][0].text(), strings.Join(dedupe(labels), ", ")))
			}
			return value{k: kBool, b: false, refs: refsOf(items...), note: "không giống nhau: " + strings.Join(parts, " ≠ ")}, nil
		}},
		"MATCH": {2, 3, "hai giá trị và cách so (\"số\", \"tên\", \"chứa\", \"gần đúng\")", func(a []value) (value, error) {
			mode := "gần đúng"
			if len(a) > 2 {
				mode = textutil.Normalize(a[2].s)
			}
			return matchAny(a[0], a[1], mode), nil
		}},
		"CONTAINS": {2, 2, "văn bản và đoạn cần tìm", func(a []value) (value, error) {
			return matchAny(a[0], a[1], "chua"), nil
		}},
		"IF": {2, 3, "điều kiện, kết quả khi đúng[, khi sai]", func(a []value) (value, error) {
			c := verdict(a[0])
			switch c.Status {
			case Pass:
				return a[1], nil
			case Fail:
				if len(a) > 2 {
					return a[2], nil
				}
				return value{k: kBool, b: true, note: "không áp dụng: " + strings.Join(failNotes(c), "; ")}, nil
			}
			return unknown(c.Missing...), nil
		}},
		"AND": {2, -1, "các điều kiện", func(a []value) (value, error) {
			v := a[0]
			for _, x := range a[1:] {
				v = logic("and", v, x)
			}
			return v, nil
		}},
		"OR": {2, -1, "các điều kiện", func(a []value) (value, error) {
			v := a[0]
			for _, x := range a[1:] {
				v = logic("or", v, x)
			}
			return v, nil
		}},
		"DATE_ADD": {3, 3, "ngày, số lượng, đơn vị (\"ngày\", \"tháng\", \"năm\")", func(a []value) (value, error) {
			if a[0].k == kList {
				out := value{k: kList}
				for _, x := range a[0].l {
					v, _ := funcs["DATE_ADD"].fn([]value{x, a[1], a[2]})
					out.l = append(out.l, v)
				}
				return out, nil
			}
			if a[0].k == kUnknown || a[1].k == kUnknown {
				return unknown(append(a[0].missing, a[1].missing...)...), nil
			}
			if a[0].k != kDate || a[1].k != kNum {
				return unknown("DATE_ADD cần ngày và số"), nil
			}
			n := int(a[1].n)
			t := a[0].t
			switch textutil.Normalize(a[2].s) {
			case "ngay", "day", "days":
				t = t.AddDate(0, 0, n)
			case "thang", "month", "months":
				t = t.AddDate(0, n, 0)
			case "nam", "year", "years":
				t = t.AddDate(n, 0, 0)
			default:
				return unknown("DATE_ADD: đơn vị phải là ngày, tháng hoặc năm"), nil
			}
			return value{k: kDate, t: t, refs: a[0].refs}, nil
		}},
		"TODAY": {0, 0, "không có tham số", func([]value) (value, error) {
			y, m, d := time.Now().Date()
			return value{k: kDate, t: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}, nil
		}},
	}
}

func failNotes(r Result) []string {
	var out []string
	for _, f := range r.Failing {
		out = append(out, f.Note)
	}
	return out
}

// pair keeps two values together through broadcast2.
func (v value) pair() value { return value{k: kStr, l: v.l} }

// matchAny reports whether a matches b by mode; a list matches when any
// item does.
func matchAny(a, b value, mode string) value {
	as, bs := flat([]value{a}), flat([]value{b})
	var missing []string
	for _, x := range as {
		for _, y := range bs {
			if x.k == kUnknown || y.k == kUnknown {
				missing = append(missing, append(x.missing, y.missing...)...)
				continue
			}
			if matches(asText(x), asText(y), mode) {
				return value{k: kBool, b: true, refs: refsOf(x, y), note: fmt.Sprintf("%s khớp %s", x.text(), y.text())}
			}
		}
	}
	if len(missing) > 0 && len(missing) == len(as)*len(bs) {
		return unknown(missing...)
	}
	return value{k: kBool, b: false, refs: refsOf(append(as, bs...)...), note: fmt.Sprintf("%s không khớp %s", a.text(), b.text())}
}

var companyWords = []string{"cong ty", "trach nhiem huu han", "tnhh", "co phan", "cp", "mot thanh vien", "mtv", "chi nhanh", "tap doan", "doanh nghiep tu nhan", "dntn"}

func companyKey(s string) string {
	s = " " + textutil.Normalize(s) + " "
	for _, w := range companyWords {
		s = strings.ReplaceAll(s, " "+w+" ", " ")
	}
	return textutil.ValueKey(s)
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func matches(a, b, mode string) bool {
	switch mode {
	case "so", "number", "digits":
		da, db := digits(a), digits(b)
		return da != "" && da == db
	case "ten", "name":
		ka, kb := companyKey(a), companyKey(b)
		return ka != "" && (ka == kb || strings.Contains(ka, kb) || strings.Contains(kb, ka))
	case "chua", "contains":
		return textutil.ValueIn(b, a)
	}
	ka, kb := textutil.ValueKey(a), textutil.ValueKey(b)
	if ka == "" || kb == "" {
		return false
	}
	return ka == kb || strings.Contains(ka, kb) || strings.Contains(kb, ka) || textutil.Similarity(ka, kb) >= 0.8
}
