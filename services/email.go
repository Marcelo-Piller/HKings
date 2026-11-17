package services

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Envio de e-mail por SMTP (pensado para o Gmail com "senha de app").
//
// Só envia com conexão criptografada: STARTTLS na porta 587 ou TLS direto na
// 465. Se o servidor não oferecer criptografia, o envio é recusado em vez de
// mandar a senha em texto aberto pela rede.

type SMTPConfig struct {
	Host     string
	Port     int
	User     string // também é o remetente: o Gmail só envia pela própria conta
	Password string
	FromName string
}

func (c SMTPConfig) Configured() bool {
	return c.Host != "" && c.Port > 0 && c.User != "" && c.Password != ""
}

type EmailAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Email struct {
	To          string
	Subject     string
	TextBody    string
	HTMLBody    string
	Attachments []EmailAttachment
}

// semQuebra recusa CR/LF em campos que viram cabeçalho. Sem essa checagem, um
// nome ou assunto com quebra de linha poderia injetar cabeçalhos, como um Bcc
// escondido para outra pessoa.
func semQuebra(campos ...string) error {
	for _, c := range campos {
		if strings.ContainsAny(c, "\r\n") {
			return fmt.Errorf("cabeçalho de e-mail com quebra de linha")
		}
	}
	return nil
}

// SendEmail envia a mensagem pelo SMTP configurado.
func SendEmail(cfg SMTPConfig, msg Email) error {
	if !cfg.Configured() {
		return fmt.Errorf("envio de e-mail não configurado")
	}
	if err := semQuebra(msg.To, msg.Subject, cfg.FromName, cfg.User); err != nil {
		return err
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("destinatário inválido")
	}
	from := mail.Address{Name: cfg.FromName, Address: cfg.User}

	body, err := buildMIME(from, *to, msg)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	if cfg.Port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("conexão com o servidor de e-mail: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()

	if cfg.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("servidor de e-mail sem STARTTLS; envio recusado para não expor a senha")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if err := client.Auth(smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)); err != nil {
		return fmt.Errorf("autenticação no servidor de e-mail recusada (confira a senha de app): %w", err)
	}
	if err := client.Mail(cfg.User); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func buildMIME(from, to mail.Address, msg Email) ([]byte, error) {
	mixed, err := boundary()
	if err != nil {
		return nil, err
	}
	alt, err := boundary()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to.String())
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", mixed, dominio(from.Address))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed)

	fmt.Fprintf(&b, "--%s\r\n", mixed)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", alt)
	escreverParte(&b, alt, "text/plain; charset=utf-8", []byte(msg.TextBody))
	escreverParte(&b, alt, "text/html; charset=utf-8", []byte(msg.HTMLBody))
	fmt.Fprintf(&b, "--%s--\r\n", alt)

	for _, a := range msg.Attachments {
		if err := semQuebra(a.Filename, a.ContentType); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "--%s\r\n", mixed)
		fmt.Fprintf(&b, "Content-Type: %s; name=%q\r\n", a.ContentType, a.Filename)
		fmt.Fprintf(&b, "Content-Disposition: attachment; filename=%q\r\n", a.Filename)
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		escreverBase64(&b, a.Data)
	}
	fmt.Fprintf(&b, "--%s--\r\n", mixed)
	return b.Bytes(), nil
}

func escreverParte(b *bytes.Buffer, boundary, contentType string, conteudo []byte) {
	fmt.Fprintf(b, "--%s\r\n", boundary)
	fmt.Fprintf(b, "Content-Type: %s\r\n", contentType)
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	escreverBase64(b, conteudo)
}

// escreverBase64 quebra em linhas de 76 caracteres, como o padrão exige.
func escreverBase64(b *bytes.Buffer, dados []byte) {
	s := base64.StdEncoding.EncodeToString(dados)
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
}

func boundary() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "ml-" + hex.EncodeToString(raw[:]), nil
}

func dominio(endereco string) string {
	if i := strings.LastIndexByte(endereco, '@'); i >= 0 {
		return endereco[i+1:]
	}
	return "localhost"
}
