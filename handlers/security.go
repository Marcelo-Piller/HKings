package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/sessions"
)

type securityContextKey string

const csrfContextKey securityContextKey = "csrf-token"
const csrfSessionKey = "csrf_token"

func isProduction() bool {
	return strings.EqualFold(os.Getenv("APP_ENV"), "production")
}

// trustProxyHeaders só deve ser ligado quando existe de fato um proxy reverso
// à frente da aplicação sanitizando X-Forwarded-For. Ligado sem proxy, qualquer
// cliente pode forjar o próprio IP e escapar do limite de tentativas de login.
func trustProxyHeaders() bool {
	return strings.EqualFold(os.Getenv("TRUSTED_PROXY"), "true")
}

// WithSecurity aplica cabeçalhos de defesa e proteção CSRF às páginas do site.
// Endpoints de webhook são autenticados por seus próprios protocolos e ficam
// explicitamente isentos, pois não recebem o cookie de sessão do comprador.
func WithSecurity(next http.Handler) http.Handler {
	return largeUploads(securityHeaders(csrfProtection(next)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// As fontes da marca (Fraunces e DM Sans) vêm do Google Fonts: a folha
		// de estilo de fonts.googleapis.com e os arquivos de fonts.gstatic.com.
		// Nenhum dos dois entra em script-src.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com; style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://cdnjs.cloudflare.com https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self'; worker-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if isProduction() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func csrfProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if csrfExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Quando o cookie não decodifica — segredo rotacionado, cookie
		// corrompido, ou de uma versão anterior — o gorilla/sessions ainda
		// devolve uma sessão nova e utilizável junto com o erro. Tratar esse
		// erro como fatal travava o site inteiro para qualquer visitante nessa
		// situação; o certo é seguir com a sessão nova, como o resto do código
		// já faz.
		sess, err := store.Get(r, "session")
		if err != nil {
			log.Printf("cookie de sessão não decodificado, iniciando nova sessão: %v", err)
		}
		token, _ := sess.Values[csrfSessionKey].(string)
		if token == "" {
			var raw [32]byte
			if _, err := rand.Read(raw[:]); err != nil {
				http.Error(w, "não foi possível iniciar a sessão", http.StatusInternalServerError)
				return
			}
			token = base64.RawURLEncoding.EncodeToString(raw[:])
			sess.Values[csrfSessionKey] = token
			if err := sess.Save(r, w); err != nil {
				http.Error(w, "não foi possível salvar a sessão", http.StatusInternalServerError)
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), csrfContextKey, token))
		if isStateChangingMethod(r.Method) {
			provided := r.FormValue("csrf_token")
			if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				http.Error(w, "requisição CSRF inválida", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrfExempt libera apenas os endpoints que não são navegados pelo browser do
// comprador e portanto nunca recebem o cookie de sessão. O webhook do Mercado
// Pago tem sua própria autenticação, por assinatura HMAC.
func csrfExempt(path string) bool {
	return path == "/webhook/mp" || path == "/healthz"
}

func isStateChangingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func csrfToken(r *http.Request) string {
	token, _ := r.Context().Value(csrfContextKey).(string)
	return token
}

// rotateCSRFToken emite um token novo quando o nível de acesso da sessão muda.
// Sem isso, um token obtido antes do login continuaria válido depois dele.
func rotateCSRFToken(sess *sessions.Session) error {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	sess.Values[csrfSessionKey] = base64.RawURLEncoding.EncodeToString(raw[:])
	return nil
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
}
