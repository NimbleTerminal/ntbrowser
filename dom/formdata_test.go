package dom

import (
	"reflect"
	"testing"
)

func entries(es []FormEntry) [][2]string {
	out := [][2]string{}
	for _, e := range es {
		out = append(out, [2]string{e.Name, e.Value})
	}
	return out
}

func TestFormDataSetBasics(t *testing.T) {
	tr := mustParse(t, `<form id=f>
<input name=t value="a b"><input type=password name=p value=s><input type=hidden name=x value=h>
<textarea name=ta>a
b</textarea>
<input type=checkbox name=c1 checked><input type=checkbox name=c2 value=v2 checked><input type=checkbox name=c3>
<input type=radio name=r value=1><input type=radio name=r value=2 checked>
<select name=s><option>one</option><option value=2 selected>two</option></select>
<select name=m multiple><option selected>a</option><option>b</option><option selected disabled>c</option><option selected>d</option></select>
<input value=noname><input name=dis disabled value=z>
<fieldset disabled><legend><input name=leg value=l></legend><input name=fs value=f></fieldset>
<input type=submit name=sub value=go><input type=file name=file><input type=reset name=rs><button type=button name=bb>b</button>
</form><input name=outside value=o>`, Limits{})
	got := entries(FormDataSet(tr, tr.ByID("f"), nil))
	want := [][2]string{
		{"t", "a b"}, {"p", "s"}, {"x", "h"}, {"ta", "a\r\nb"},
		{"c1", "on"}, {"c2", "v2"}, {"r", "2"}, {"s", "2"}, {"m", "a"}, {"m", "d"}, {"leg", "l"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestFormDataSetSubmitter(t *testing.T) {
	tr := mustParse(t, `<form id=f><input name=q value=x><button name=go value=1>Go</button><button name=other value=2>O</button><input type=submit name=s value=S></form>`, Limits{})
	buttons, _ := tr.Query(tr.Root(), "button", true)
	got := entries(FormDataSet(tr, tr.ByID("f"), buttons[0]))
	if want := [][2]string{{"q", "x"}, {"go", "1"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestFormAttributeOwnership(t *testing.T) {
	tr := mustParse(t, `<form id=f><input name=a value=1></form><input form=f name=x value=2><input name=y value=3>`, Limits{})
	if got := entries(FormDataSet(tr, tr.ByID("f"), nil)); !reflect.DeepEqual(got, [][2]string{{"a", "1"}, {"x", "2"}}) {
		t.Fatalf("got %q", got)
	}
	if n := len(FormControls(tr, tr.ByID("f"))); n != 2 {
		t.Fatalf("FormControls %d", n)
	}
}

func TestDefaultButtonAndBlocking(t *testing.T) {
	tr := mustParse(t, `<form id=f><input id=q><button type=button>no</button><button id=b1>yes</button><input type=submit id=b2></form><form id=g><input></form>`, Limits{})
	if b := DefaultButton(tr, tr.ByID("f")); b != tr.ByID("b1") {
		t.Fatalf("default button %v", b)
	}
	if b := DefaultButton(tr, tr.ByID("g")); b != nil {
		t.Fatal("form without submit button")
	}
	tr2 := mustParse(t, `<input id=t><input id=c type=checkbox><textarea id=a></textarea><input id=n type=number><input id=u type=weird>`, Limits{})
	for id, want := range map[string]bool{"t": true, "c": false, "a": false, "n": true, "u": true} {
		if got := BlocksImplicitSubmission(tr2.ByID(id)); got != want {
			t.Errorf("%s: %v", id, got)
		}
	}
}

func TestEncode(t *testing.T) {
	got := Encode([]FormEntry{{"q", "hello world"}, {"x", "a&b=c"}, {"z", "é"}})
	if want := "q=hello+world&x=a%26b%3Dc&z=%C3%A9"; got != want {
		t.Fatalf("%q", got)
	}
}
