package handlers

import (
	"crypto/rand"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/sessions"
)

func sessionSecret() []byte {
	if secret := os.Getenv("SESSION_SECRET"); secret != "" {
		if isProduction() && len(secret) < 32 {
			log.Fatal("SESSION_SECRET deve ter pelo menos 32 caracteres em produção")
		}
		return []byte(secret)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		log.Fatal("não foi possível gerar segredo da sessão:", err)
	}
	if isProduction() {
		log.Fatal("SESSION_SECRET é obrigatório em produção")
	}
	log.Print("SESSION_SECRET não configurado; usando segredo efêmero de desenvolvimento")
	return secret
}

var store = sessions.NewCookieStore(sessionSecret())

func init() {
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 dias
		HttpOnly: true,
		Secure:   isProduction(),
		SameSite: http.SameSiteLaxMode,
	}
}
