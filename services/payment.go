package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// InfinitPay REST API — documentação: https://developers.infinitpay.io
const infinitPayBaseURL = "https://api.infinitpay.io/v1"

type paymentLinkRequest struct {
	Amount      int64  `json:"amount"`      // valor em centavos (ex: R$99,90 → 9990)
	Description string `json:"description"`
	ExternalID  string `json:"external_id"` // seu ID interno do pedido
	Currency    string `json:"currency"`    // "BRL"
}

type paymentLinkResponse struct {
	ID         string `json:"id"`
	PaymentURL string `json:"payment_url"`
	Status     string `json:"status"`
	Message    string `json:"message"` // campo de erro da API
}

// CreatePaymentLink cria um link de pagamento no InfinitPay e retorna a URL.
// Configure a variável de ambiente INFINITPAY_API_KEY com sua chave de API.
// Se a chave não estiver configurada, retorna erro e o checkout usa fluxo padrão.
func CreatePaymentLink(amountBRL float64, description string, orderID int64) (string, error) {
	apiKey := os.Getenv("INFINITPAY_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("INFINITPAY_API_KEY não configurada")
	}

	payload := paymentLinkRequest{
		Amount:      int64(amountBRL * 100),
		Description: description,
		ExternalID:  fmt.Sprintf("pedido-%d", orderID),
		Currency:    "BRL",
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", infinitPayBaseURL+"/payment_links", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao chamar InfinitPay: %w", err)
	}
	defer resp.Body.Close()

	var result paymentLinkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("erro ao decodificar resposta: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("InfinitPay retornou %d: %s", resp.StatusCode, result.Message)
	}

	if result.PaymentURL == "" {
		return "", fmt.Errorf("InfinitPay não retornou URL de pagamento")
	}

	return result.PaymentURL, nil
}
