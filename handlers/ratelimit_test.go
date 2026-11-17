package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBloqueiaAposLimite(t *testing.T) {
	limiter := newRateLimiter(3, time.Minute, time.Minute)
	const key = "admin|10.0.0.1"

	for i := 0; i < 2; i++ {
		limiter.RegisterFailure(key)
		if allowed, _ := limiter.Allowed(key); !allowed {
			t.Fatalf("bloqueou cedo demais, na tentativa %d", i+1)
		}
	}

	limiter.RegisterFailure(key) // terceira falha atinge o limite
	allowed, retryIn := limiter.Allowed(key)
	if allowed {
		t.Fatal("não bloqueou após atingir o limite de tentativas")
	}
	if retryIn <= 0 {
		t.Fatalf("tempo de espera inválido: %v", retryIn)
	}
}

func TestRateLimiterLiberaAposLoginBemSucedido(t *testing.T) {
	limiter := newRateLimiter(3, time.Minute, time.Minute)
	const key = "seller|10.0.0.2"

	limiter.RegisterFailure(key)
	limiter.RegisterFailure(key)
	limiter.Reset(key)
	limiter.RegisterFailure(key)

	if allowed, _ := limiter.Allowed(key); !allowed {
		t.Fatal("contagem antiga persistiu após login bem-sucedido")
	}
}

func TestRateLimiterIsolaEscopos(t *testing.T) {
	limiter := newRateLimiter(2, time.Minute, time.Minute)

	limiter.RegisterFailure("admin|10.0.0.3")
	limiter.RegisterFailure("admin|10.0.0.3")

	if allowed, _ := limiter.Allowed("admin|10.0.0.3"); allowed {
		t.Fatal("escopo admin deveria estar bloqueado")
	}
	// Bloquear o painel do admin não pode derrubar o login da loja.
	if allowed, _ := limiter.Allowed("customer|10.0.0.3"); !allowed {
		t.Fatal("bloqueio vazou para outro escopo")
	}
}

func TestClientIPIgnoraCabecalhoForjadoSemProxy(t *testing.T) {
	t.Setenv("TRUSTED_PROXY", "false")

	r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	r.RemoteAddr = "203.0.113.10:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	// Sem proxy à frente, confiar no cabeçalho permitiria trocar de "IP" a cada
	// tentativa e escapar do limite de login.
	if got := clientIP(r); got != "203.0.113.10" {
		t.Fatalf("clientIP = %q; cabeçalho forjado foi considerado", got)
	}
}

func TestClientIPUsaCabecalhoComProxyConfiavel(t *testing.T) {
	t.Setenv("TRUSTED_PROXY", "true")

	r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	if got := clientIP(r); got != "1.2.3.4" {
		t.Fatalf("clientIP = %q; esperado o primeiro endereço da cadeia", got)
	}
}
