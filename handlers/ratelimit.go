package handlers

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Sem limite de tentativas, as telas de login de admin, vendedor e cliente
// aceitam adivinhação de senha na velocidade da rede. O contador abaixo é por
// processo e em memória, o que é suficiente para a loja rodar em um único
// container e não adiciona dependência externa.

type attemptCounter struct {
	failures  int
	firstSeen time.Time
	blockedTo time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptCounter
	limit    int
	window   time.Duration
	penalty  time.Duration
	lastGC   time.Time
}

func newRateLimiter(limit int, window, penalty time.Duration) *rateLimiter {
	return &rateLimiter{
		attempts: make(map[string]*attemptCounter),
		limit:    limit,
		window:   window,
		penalty:  penalty,
		lastGC:   time.Now(),
	}
}

// loginLimiter bloqueia por 15 minutos após 5 falhas em 15 minutos.
var loginLimiter = newRateLimiter(5, 15*time.Minute, 15*time.Minute)

// registerLimiter conta toda tentativa de autocadastro de vendedor, aprovada
// ou não — diferente do login, aqui nunca há Reset. Sem isso, o formulário de
// cadastro seria uma porta aberta para criar contas em massa por script.
var registerLimiter = newRateLimiter(10, 30*time.Minute, 30*time.Minute)

// Allowed informa se a chave ainda pode tentar, e quanto falta do bloqueio.
func (rl *rateLimiter) Allowed(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.collectLocked()

	entry, ok := rl.attempts[key]
	if !ok {
		return true, 0
	}
	if now := time.Now(); now.Before(entry.blockedTo) {
		return false, time.Until(entry.blockedTo)
	}
	return true, 0
}

// RegisterFailure contabiliza uma tentativa malsucedida.
func (rl *rateLimiter) RegisterFailure(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.collectLocked()

	now := time.Now()
	entry, ok := rl.attempts[key]
	if !ok || now.Sub(entry.firstSeen) > rl.window {
		rl.attempts[key] = &attemptCounter{failures: 1, firstSeen: now}
		return
	}
	entry.failures++
	if entry.failures >= rl.limit {
		entry.blockedTo = now.Add(rl.penalty)
		entry.failures = 0
		entry.firstSeen = now
	}
}

// Reset limpa o histórico após um login bem-sucedido, para que o usuário
// legítimo não carregue tentativas antigas.
func (rl *rateLimiter) Reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, key)
}

// collectLocked descarta entradas vencidas para o mapa não crescer sem limite.
// Deve ser chamada com o mutex já travado.
func (rl *rateLimiter) collectLocked() {
	now := time.Now()
	if now.Sub(rl.lastGC) < time.Minute {
		return
	}
	rl.lastGC = now
	for key, entry := range rl.attempts {
		if now.After(entry.blockedTo) && now.Sub(entry.firstSeen) > rl.window {
			delete(rl.attempts, key)
		}
	}
}

// clientIP identifica o cliente para o limite de tentativas. X-Forwarded-For só
// é considerado quando TRUSTED_PROXY está ligado, porque o cabeçalho é enviado
// pelo próprio cliente e, sem um proxy à frente, permitiria burlar o bloqueio
// trocando o valor a cada tentativa.
func clientIP(r *http.Request) string {
	if trustProxyHeaders() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if first, _, found := strings.Cut(forwarded, ","); found {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(forwarded)
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginRateKey combina o escopo com o IP, para que o bloqueio do painel do
// admin não derrube o login da loja e vice-versa.
func loginRateKey(scope string, r *http.Request) string {
	return scope + "|" + clientIP(r)
}
