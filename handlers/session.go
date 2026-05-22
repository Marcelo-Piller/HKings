package handlers

import (
	"github.com/gorilla/sessions"
)

var store = sessions.NewCookieStore([]byte("loja-secret-key-mude-em-producao"))

func init() {
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 dias
		HttpOnly: true,
	}
}
