package models

import "testing"

func TestFormatBRL(t *testing.T) {
	casos := []struct {
		valor float64
		quer  string
	}{
		{0, "R$ 0,00"},
		{0.5, "R$ 0,50"},
		{79.9, "R$ 79,90"},
		{1234.56, "R$ 1.234,56"},
		{1000000, "R$ 1.000.000,00"},
		{0.1 + 0.2, "R$ 0,30"}, // soma de ponto flutuante não pode vazar
		{-15.5, "-R$ 15,50"},
	}
	for _, c := range casos {
		if got := FormatBRL(c.valor); got != c.quer {
			t.Errorf("FormatBRL(%v) = %q; quer %q", c.valor, got, c.quer)
		}
	}
}
