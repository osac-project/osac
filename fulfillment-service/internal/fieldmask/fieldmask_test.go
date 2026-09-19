package fieldmask

import "testing"

func TestIsCanonicalPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "spec.add_on_operators", want: true},
		{path: "spec .add_on_operators", want: false},
		{path: "spec.add_on_operators.-1", want: false},
		{path: "spec..add_on_operators", want: false},
	}
	for _, test := range tests {
		if got := IsCanonicalPath(test.path); got != test.want {
			t.Errorf("IsCanonicalPath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}
