package models

import "sort"

// Ordem da vitrine: livros de vendedores com melhor reputação aparecem
// primeiro. O peso vem do nível (Esmeralda > Ouro > Prata > Bronze > sem
// avaliações), sobe com a medalha Envio Rápido e cai quando o vendedor está
// com exposição reduzida por atrasos. Empates mantêm a ordem de cadastro.

var tierWeight = map[string]float64{"esmeralda": 5, "ouro": 4, "prata": 3, "bronze": 2, "cinza": 1}

// siteWeight é o peso dos livros vendidos pelo próprio Livredo: no nível de
// um vendedor Prata, sem passar à frente de quem tem ótima reputação.
const siteWeight = 3

// RelevanceScores calcula o peso de cada vendedor da lista.
func RelevanceScores(sellerIDs []int, badges map[int]SellerBadges) map[int]float64 {
	scores := map[int]float64{0: siteWeight}
	for _, id := range sellerIDs {
		if _, ok := scores[id]; ok {
			continue
		}
		score := tierWeight[GetSellerRating(id).Tier.Key]
		if b := badges[id]; b.FastShipper {
			score += 1
		} else if b.ReducedExposure {
			score -= 3
		}
		scores[id] = score
	}
	return scores
}

// SortByRelevance ordena os livros pelo peso do vendedor.
func SortByRelevance(products []Product) {
	ids := make([]int, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.SellerID)
	}
	badges := ShippingBadges(ids)
	scores := RelevanceScores(ids, badges)
	for i := range products {
		products[i].FastShipper = badges[products[i].SellerID].FastShipper
	}
	sort.SliceStable(products, func(i, j int) bool {
		return scores[products[i].SellerID] > scores[products[j].SellerID]
	})
}
