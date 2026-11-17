package models

import (
	"math"
	"strconv"
	"strings"
)

// FormatBRL escreve um valor no formato brasileiro: R$ 1.234,56.
//
// O cálculo passa por centavos inteiros para que 0,1 + 0,2 não apareça como
// R$ 0,30000000004 nem arredonde para o lado errado.
func FormatBRL(value float64) string {
	cents := int64(math.Round(value * 100))
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}

	reais := strconv.FormatInt(cents/100, 10)
	var grouped strings.Builder
	for i, digit := range reais {
		if i > 0 && (len(reais)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(digit)
	}

	centavos := cents % 100
	pad := ""
	if centavos < 10 {
		pad = "0"
	}
	return sign + "R$ " + grouped.String() + "," + pad + strconv.FormatInt(centavos, 10)
}
