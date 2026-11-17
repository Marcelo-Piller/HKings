package models

import (
	"math"
	"strconv"
	"strings"
)

// Comissão do site por vendedor.
//
// A comissão base é a de Configurações (comissao_percentual). Vendedores nos
// níveis mais altos ganham um desconto em pontos percentuais: por padrão, 2
// no Ouro e 4 na Esmeralda (10% vira 8% e 6%). O nível depende só da média,
// mas o desconto é dinheiro, então só vale a partir de MinRatedSalesForDiscount
// vendas avaliadas: sem esse mínimo, uma única nota 5 dada por um amigo já
// reduziria a comissão de todas as vendas.

// MinRatedSalesForDiscount é o mínimo de vendas avaliadas para o desconto.
const MinRatedSalesForDiscount = 10

// tierDiscountDefaults são os descontos padrão, em pontos percentuais.
var tierDiscountDefaults = map[string]float64{
	"bronze":    0,
	"prata":     0,
	"ouro":      2,
	"esmeralda": 4,
}

// TierDiscountSettingKey é a configuração do desconto de um nível.
func TierDiscountSettingKey(tierKey string) string {
	return "desconto_" + tierKey
}

// TierCommissionDiscount devolve o desconto do nível, em pontos percentuais.
func TierCommissionDiscount(tierKey string) float64 {
	padrao, ok := tierDiscountDefaults[tierKey]
	if !ok {
		return 0
	}
	raw := strings.TrimSpace(strings.ReplaceAll(GetSetting(TierDiscountSettingKey(tierKey)), ",", "."))
	if raw == "" {
		return padrao
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 50 || math.IsNaN(v) {
		return padrao
	}
	return v
}

// SellerCommission descreve a comissão que o site retém de um vendedor.
type SellerCommission struct {
	Base      float64 // comissão de Configurações
	Discount  float64 // desconto aplicado, em pontos percentuais
	Effective float64 // o que é retido de fato
	Rating    SellerRating
	// TierDiscount é o desconto do nível atual, mesmo que ainda não valha.
	TierDiscount float64
	// MissingReviews: quantas vendas avaliadas faltam para o desconto valer.
	MissingReviews int
}

// GetSellerCommission calcula a comissão atual de um vendedor.
func GetSellerCommission(sellerID int) SellerCommission {
	c := SellerCommission{Base: MarketplaceFeePercent(), Rating: GetSellerRating(sellerID)}
	c.TierDiscount = TierCommissionDiscount(c.Rating.Tier.Key)
	if c.Rating.Count >= MinRatedSalesForDiscount {
		c.Discount = math.Min(c.TierDiscount, c.Base)
	} else if c.TierDiscount > 0 {
		c.MissingReviews = MinRatedSalesForDiscount - c.Rating.Count
	}
	c.Effective = c.Base - c.Discount
	return c
}

// SellerFeePercent é a comissão a reter numa venda do vendedor, em %.
func SellerFeePercent(sellerID int) float64 {
	return GetSellerCommission(sellerID).Effective
}

// PercentLabel formata um percentual no padrão brasileiro: 7,5.
func PercentLabel(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// TierBenefit é uma linha da tabela "como subir de nível".
type TierBenefit struct {
	Tier       SellerTier
	MinAverage string // "4,8"
	Discount   float64
}

// TierBenefits lista os níveis do mais baixo ao mais alto, com o desconto de
// cada um.
func TierBenefits() []TierBenefit {
	out := make([]TierBenefit, 0, len(sellerTiers))
	for i := len(sellerTiers) - 1; i >= 0; i-- {
		t := sellerTiers[i]
		out = append(out, TierBenefit{
			Tier:       t.tier,
			MinAverage: strings.Replace(strconv.FormatFloat(t.minAverage, 'f', 1, 64), ".", ",", 1),
			Discount:   TierCommissionDiscount(t.tier.Key),
		})
	}
	return out
}
