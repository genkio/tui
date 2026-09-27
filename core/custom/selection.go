package custom

import (
	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
)

// selection is what $ returns: cheerio's API, or the part of it a parser
// ported from an RSSHub route reaches for. Method names reach the script
// lower-camel (Find is find) through the engine's field mapper.
//
// Where it differs: map returns a plain array, not a selection to .get() one
// out of, and children and the traversals take no filter.
type selection struct {
	r      *runtime
	s      *goquery.Selection
	Length int
}

func (r *runtime) wrap(s *goquery.Selection) *selection {
	return &selection{r: r, s: s, Length: s.Length()}
}

func (x *selection) Find(q string) *selection    { return x.r.wrap(x.s.Find(q)) }
func (x *selection) Filter(q string) *selection  { return x.r.wrap(x.s.Filter(q)) }
func (x *selection) Closest(q string) *selection { return x.r.wrap(x.s.Closest(q)) }
func (x *selection) Children() *selection        { return x.r.wrap(x.s.Children()) }
func (x *selection) Parent() *selection          { return x.r.wrap(x.s.Parent()) }
func (x *selection) Next() *selection            { return x.r.wrap(x.s.Next()) }
func (x *selection) Prev() *selection            { return x.r.wrap(x.s.Prev()) }
func (x *selection) First() *selection           { return x.r.wrap(x.s.First()) }
func (x *selection) Last() *selection            { return x.r.wrap(x.s.Last()) }
func (x *selection) Eq(i int) *selection         { return x.r.wrap(x.s.Eq(i)) }
func (x *selection) Is(q string) bool            { return x.s.Is(q) }
func (x *selection) HasClass(c string) bool      { return x.s.HasClass(c) }
func (x *selection) Text() string                { return x.s.Text() }

// Html is the first element's inner HTML, or null when nothing matched, as
// under cheerio, so an optional chain after it reads the same.
func (x *selection) Html() goja.Value {
	if x.Length == 0 {
		return goja.Null()
	}
	h, err := x.s.Html()
	if err != nil {
		return goja.Null()
	}
	return x.r.vm.ToValue(h)
}

// Attr is the first element's attribute, or undefined when it has none.
func (x *selection) Attr(name string) goja.Value {
	v, ok := x.s.Attr(name)
	if !ok {
		return goja.Undefined()
	}
	return x.r.vm.ToValue(v)
}

func (x *selection) ToArray() goja.Value {
	els := make([]any, x.Length)
	for i := range els {
		els[i] = x.r.wrap(x.s.Eq(i))
	}
	return x.r.vm.NewArray(els...)
}

// Each calls fn(index, element) with this bound to the element, and stops
// early when fn returns false.
func (x *selection) Each(fn goja.Callable) *selection {
	for i := 0; i < x.Length; i++ {
		el := x.r.vm.ToValue(x.r.wrap(x.s.Eq(i)))
		v, err := fn(el, x.r.vm.ToValue(i), el)
		if err != nil {
			panic(err)
		}
		if v != nil && v.StrictEquals(x.r.vm.ToValue(false)) {
			break
		}
	}
	return x
}

// Map calls fn(index, element) the same way and collects what it returns,
// leaving out null and undefined as cheerio does.
func (x *selection) Map(fn goja.Callable) goja.Value {
	var out []any
	for i := 0; i < x.Length; i++ {
		el := x.r.vm.ToValue(x.r.wrap(x.s.Eq(i)))
		v, err := fn(el, x.r.vm.ToValue(i), el)
		if err != nil {
			panic(err)
		}
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			continue
		}
		out = append(out, v)
	}
	return x.r.vm.NewArray(out...)
}
