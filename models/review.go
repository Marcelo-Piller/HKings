package models

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/vendas/loja/database"
)

// Avaliação de vendedores por estrelas.
//
// Só quem comprou avalia, uma vez por pedido, e só depois do envio. O nível
// (cinza < bronze < prata < ouro < esmeralda) depende só da média: cada
// avaliação ruim a puxa para baixo. O desconto na comissão, que é dinheiro,
// exige também um mínimo de vendas avaliadas (ver commission.go).

var (
	ErrReviewNotAllowed = errors.New("este pedido não pode ser avaliado")
	ErrAlreadyReviewed  = errors.New("este pedido já foi avaliado")
)

const maxReviewComment = 500

// SellerTier é um degrau da escala de reputação.
type SellerTier struct {
	Key   string // cinza, bronze, prata, ouro, esmeralda
	Label string
}

// sellerTiers em ordem decrescente: vale o primeiro cuja média é atingida.
var sellerTiers = []struct {
	tier       SellerTier
	minAverage float64
}{
	{SellerTier{"esmeralda", "Esmeralda"}, 4.8},
	{SellerTier{"ouro", "Ouro"}, 4.4},
	{SellerTier{"prata", "Prata"}, 3.8},
	{SellerTier{"bronze", "Bronze"}, 3.0},
}

var tierCinza = SellerTier{"cinza", "Cinza"}

// TierFor escolhe o nível pela média. Sem nenhuma avaliação, o vendedor
// fica no Cinza: ainda não há média.
func TierFor(average float64, count int) SellerTier {
	if count == 0 {
		return tierCinza
	}
	for _, t := range sellerTiers {
		if average >= t.minAverage {
			return t.tier
		}
	}
	return tierCinza
}

// SellerRating resume a reputação de um vendedor.
type SellerRating struct {
	Average float64 // 0 quando não há avaliações
	Count   int
	Tier    SellerTier
}

// AverageLabel formata a média no padrão brasileiro: "4,7".
func (r SellerRating) AverageLabel() string {
	return strings.Replace(fmt.Sprintf("%.1f", r.Average), ".", ",", 1)
}

// Stars devolve as 5 estrelas para desenhar: "cheia", "meia" ou "vazia".
func (r SellerRating) Stars() []string {
	stars := make([]string, 5)
	arredondada := math.Round(r.Average*2) / 2
	for i := range stars {
		switch {
		case arredondada >= float64(i+1):
			stars[i] = "cheia"
		case arredondada >= float64(i)+0.5:
			stars[i] = "meia"
		default:
			stars[i] = "vazia"
		}
	}
	return stars
}

func GetSellerRating(sellerID int) SellerRating {
	var avg float64
	var count int
	database.DB.QueryRow(`SELECT COALESCE(AVG(stars), 0), COUNT(*) FROM seller_reviews WHERE seller_id=?`, sellerID).Scan(&avg, &count)
	return SellerRating{Average: avg, Count: count, Tier: TierFor(avg, count)}
}

// Review é a avaliação de um pedido.
type Review struct {
	OrderID   int
	Stars     int
	Comment   string
	CreatedAt string
}

// GetReviewForOrder devolve a avaliação do pedido, ou nil se ainda não houver.
func GetReviewForOrder(orderID int) *Review {
	var r Review
	err := database.DB.QueryRow(`SELECT order_id, stars, comment, created_at FROM seller_reviews WHERE order_id=?`, orderID).
		Scan(&r.OrderID, &r.Stars, &r.Comment, &r.CreatedAt)
	if err != nil {
		return nil
	}
	return &r
}

// CanReview diz se o cliente pode avaliar o pedido agora.
func CanReview(order *Order, customerID int) bool {
	return order != nil && customerID > 0 && order.CustomerID == customerID &&
		order.SellerID != 0 && (order.Status == "enviado" || order.Status == "entregue")
}

// CreateReview grava a avaliação, conferindo de novo todas as regras: o
// formulário pode ser forjado, o banco não aceita.
func CreateReview(orderID, customerID, stars int, comment string) error {
	order, err := GetOrder(orderID)
	if err != nil || !CanReview(order, customerID) {
		return ErrReviewNotAllowed
	}
	if stars < 1 || stars > 5 {
		return fmt.Errorf("%w: escolha de 1 a 5 estrelas", ErrReviewNotAllowed)
	}
	comment = strings.TrimSpace(comment)
	if len([]rune(comment)) > maxReviewComment {
		comment = string([]rune(comment)[:maxReviewComment])
	}
	_, err = database.DB.Exec(
		`INSERT INTO seller_reviews (order_id, seller_id, customer_id, stars, comment) VALUES (?, ?, ?, ?, ?)`,
		orderID, order.SellerID, customerID, stars, comment,
	)
	if err != nil && isUniqueConstraintErr(err) {
		return ErrAlreadyReviewed
	}
	return err
}
