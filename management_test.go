package main

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"personal", "personal"},
		{"Team 2", "team-2"},
		{"  spaced out  ", "spaced-out"},
		{"Ünïcôde!!", "n-c-de"},
		{"", ""},
		{"---", ""},
		{"already-a-slug", "already-a-slug"},
		{"a..b..c", "a-b-c"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
