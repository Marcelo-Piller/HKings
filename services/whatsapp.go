package services

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SendWhatsApp envia uma mensagem via CallMeBot (gratuito para uso pessoal).
//
// Setup único (uma vez por número):
//  1. Salve o número +34 644 82 56 81 nos seus contatos como "CallMeBot"
//  2. Envie a mensagem "I allow callmebot to send me messages" para esse número no WhatsApp
//  3. O bot responderá com sua API Key
//  4. Configure essa API Key em Configurações > WhatsApp API Key no painel admin
func SendWhatsApp(phone, apiKey, message string) error {
	if phone == "" || apiKey == "" {
		return fmt.Errorf("whatsapp: phone ou apiKey não configurados")
	}

	endpoint := fmt.Sprintf(
		"https://api.callmebot.com/whatsapp.php?phone=%s&text=%s&apikey=%s",
		url.QueryEscape(phone),
		url.QueryEscape(message),
		url.QueryEscape(apiKey),
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("whatsapp: erro ao enviar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("whatsapp: callmebot retornou status %d", resp.StatusCode)
	}
	return nil
}
