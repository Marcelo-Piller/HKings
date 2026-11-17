package handlers

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Prévia do parcelamento, mostrada na página do livro e no checkout antes de
// o cliente ir para a página do Mercado Pago, onde de fato escolhe as parcelas.
//
// As taxas e o valor mínimo por parcela dependem da conta que recebe (a do
// site ou a do vendedor), então a consulta usa o token dessa conta. O
// resultado fica em memória por uma hora: as taxas mudam raramente e não
// faz sentido chamar o Mercado Pago a cada visita.

const installmentsCacheTTL = time.Hour

type installmentsCacheEntry struct {
	plan    *services.InstallmentPlan
	expires time.Time
}

var installmentsCache = struct {
	sync.Mutex
	entries map[string]installmentsCacheEntry
}{entries: map[string]installmentsCacheEntry{}}

// installmentPreview devolve a maior opção de parcelamento para o valor, ou
// nil quando não há parcelamento (valor baixo) ou a consulta falhou — nesses
// casos a página simplesmente não mostra a prévia.
func installmentPreview(sellerID int, amount float64) *services.InstallmentPlan {
	if amount <= 0 {
		return nil
	}
	key := fmt.Sprintf("%d:%d", sellerID, int64(math.Round(amount*100)))

	installmentsCache.Lock()
	if entry, ok := installmentsCache.entries[key]; ok && time.Now().Before(entry.expires) {
		installmentsCache.Unlock()
		return entry.plan
	}
	installmentsCache.Unlock()

	var token string
	if sellerID == 0 {
		token = models.GetSetting("mp_access_token")
	} else if t, _, err := sellerAccessToken(sellerID); err == nil {
		token = t
	}
	if token == "" {
		return nil
	}

	plan, err := services.GetInstallmentPlan(token, amount, maxInstallments)
	if err != nil {
		log.Printf("prévia de parcelas: %v", err)
		return nil
	}
	if plan.Installments <= 1 {
		plan = nil // à vista não é "parcelamento"
	}

	installmentsCache.Lock()
	// Um valor distinto por preço de livro: o mapa cresce pouco, mas ganha um
	// teto para nunca virar vazamento de memória.
	if len(installmentsCache.entries) > 5000 {
		installmentsCache.entries = map[string]installmentsCacheEntry{}
	}
	installmentsCache.entries[key] = installmentsCacheEntry{plan: plan, expires: time.Now().Add(installmentsCacheTTL)}
	installmentsCache.Unlock()
	return plan
}
