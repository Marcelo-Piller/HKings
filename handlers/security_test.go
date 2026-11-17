package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vendas/loja/models"
)

func TestCSRFProtectionRejectsMissingTokenAndAcceptsSessionToken(t *testing.T) {
	protected := WithSecurity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, csrfToken(r))
	}))

	initialRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	initialResponse := httptest.NewRecorder()
	protected.ServeHTTP(initialResponse, initialRequest)
	if initialResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d", initialResponse.Code)
	}
	token := initialResponse.Body.String()
	if token == "" {
		t.Fatal("token CSRF ausente")
	}
	cookies := initialResponse.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("cookie de sessão ausente")
	}

	missingToken := httptest.NewRequest(http.MethodPost, "/", nil)
	missingToken.AddCookie(cookies[0])
	missingResponse := httptest.NewRecorder()
	protected.ServeHTTP(missingResponse, missingToken)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("POST sem CSRF status = %d, esperado %d", missingResponse.Code, http.StatusForbidden)
	}

	form := url.Values{"csrf_token": {token}}
	validRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	validRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	validRequest.AddCookie(cookies[0])
	validResponse := httptest.NewRecorder()
	protected.ServeHTTP(validResponse, validRequest)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("POST com CSRF válido status = %d", validResponse.Code)
	}
	if validResponse.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("cabeçalho anti-frame ausente")
	}
}

func TestGuestOrderAccessIsBoundToSession(t *testing.T) {
	order := &models.Order{ID: 42}
	anonymous := httptest.NewRequest(http.MethodGet, "/pedido?id=42", nil)
	if canAccessOrder(anonymous, order) {
		t.Fatal("visitante anônimo acessou pedido sem vínculo de sessão")
	}

	response := httptest.NewRecorder()
	rememberGuestOrder(response, anonymous, order.ID)
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("vínculo de pedido não criou cookie de sessão")
	}
	owner := httptest.NewRequest(http.MethodGet, "/pedido?id=42", nil)
	owner.AddCookie(cookies[0])
	if !canAccessOrder(owner, order) {
		t.Fatal("comprador não acessou seu próprio pedido")
	}
}
