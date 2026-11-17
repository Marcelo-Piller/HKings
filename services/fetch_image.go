package services

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// FetchImage baixa a imagem de uma capa informada por URL (planilha do
// lojista ou consulta de ISBN). Como o endereço vem de fora, a conexão só é
// aberta para IPs públicos: nada de localhost, rede interna do Docker ou
// endereços de metadados da nuvem, nem depois de redirecionamentos.
//
// Devolve os bytes e o tipo detectado pelo conteúdo (image/jpeg, image/png,
// image/webp ou image/gif).
func FetchImage(rawURL string, maxBytes int64) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("endereço da imagem inválido")
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Livredo/1.0 (+https://livredo.com.br)")
	req.Header.Set("Accept", "image/*")
	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("não foi possível baixar a imagem: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("a imagem respondeu HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", errors.New("imagem grande demais")
	}
	ct := http.DetectContentType(data)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch ct {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return data, ct, nil
	}
	return nil, "", errors.New("o endereço não é de uma imagem JPEG, PNG, WebP ou GIF")
}

// AllowPrivateImageHosts libera IPs internos. Só para testes (httptest
// escuta em 127.0.0.1).
var AllowPrivateImageHosts = false

var errPrivateHost = errors.New("endereço interno não permitido")

func publicIPOnly(network, address string, _ syscall.RawConn) error {
	if AllowPrivateImageHosts {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return errPrivateHost
	}
	// 100.64.0.0/10 (CGNAT) e 0.0.0.0/8 também não são internet pública.
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64)) {
		return errPrivateHost
	}
	return nil
}

var imageClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		// O Control roda depois da resolução de DNS, no IP de fato
		// conectado: um domínio que aponta para 127.0.0.1 também é barrado.
		DialContext:           (&net.Dialer{Timeout: 6 * time.Second, Control: publicIPOnly}).DialContext,
		Proxy:                 nil,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   4,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("redirecionamentos demais")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("redirecionamento inválido")
		}
		return nil
	},
}
