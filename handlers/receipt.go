package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Comprovante de compra: download em PDF e envio por e-mail.
//
// O comprovante só existe para pedido pago, só é entregue a quem pode ver o
// pedido (o cliente dono, ou o comprador na mesma sessão) e só é enviado para
// o e-mail do próprio pedido — nunca para um endereço digitado na hora, o que
// transformaria o botão em um disparador de e-mails para terceiros.

// receiptEmailLimiter: no máximo 3 envios por pedido a cada hora.
var receiptEmailLimiter = newRateLimiter(3, time.Hour, time.Hour)

var errSMTPNotConfigured = errors.New("envio de e-mail não configurado")

func smtpConfig() services.SMTPConfig {
	port, err := strconv.Atoi(strings.TrimSpace(models.GetSetting("smtp_port")))
	if err != nil || port <= 0 {
		port = 587
	}
	host := strings.TrimSpace(models.GetSetting("smtp_host"))
	if host == "" {
		host = "smtp.gmail.com"
	}
	name := models.GetSetting("store_name")
	if name == "" {
		name = "Livredo"
	}
	return services.SMTPConfig{
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(models.GetSetting("smtp_user")),
		Password: strings.ReplaceAll(models.GetSetting("smtp_password"), " ", ""), // a senha de app do Google vem em blocos com espaço
		FromName: name,
	}
}

var fusoBrasilia = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*60*60)
}()

// formatarData converte a data do banco (UTC) para o horário de Brasília.
func formatarData(valor string) string {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z", "2006-01-02 15:04:05-07:00"} {
		if t, err := time.Parse(layout, valor); err == nil {
			return t.In(fusoBrasilia).Format("02/01/2006 às 15:04")
		}
	}
	return valor
}

func buildReceipt(order *models.Order) services.Receipt {
	storeName := models.GetSetting("store_name")
	if storeName == "" {
		storeName = "Livredo"
	}
	receipt := services.Receipt{
		StoreName:     storeName,
		OrderID:       order.ID,
		CreatedAt:     formatarData(order.CreatedAt),
		PaidAt:        formatarData(order.PaidAt),
		PaymentMethod: order.PaymentMethodLabel(),
		CustomerName:  order.CustomerName,
		CustomerEmail: order.CustomerEmail,
		Address:       order.Address,
		SellerName:    storeName,
		Total:         order.FormattedTotal(),
	}
	if order.SellerID != 0 {
		if seller, err := models.GetSeller(order.SellerID); err == nil {
			receipt.SellerName = seller.Name
			receipt.SellerKind = seller.Kind()
		}
	}
	for _, item := range order.Items {
		receipt.Items = append(receipt.Items, services.ReceiptItem{
			Title:     item.ProductName,
			Quantity:  item.Quantity,
			UnitPrice: models.FormatBRL(item.Price),
			Subtotal:  models.FormatBRL(item.Price * float64(item.Quantity)),
		})
	}
	// O frete entra como linha própria, para os itens somarem o total pago.
	if order.Shipping.Cents > 0 {
		receipt.Items = append(receipt.Items, services.ReceiptItem{
			Title:     "Frete — " + order.Shipping.Service,
			Quantity:  1,
			UnitPrice: order.FormattedShipping(),
			Subtotal:  order.FormattedShipping(),
		})
	}
	return receipt
}

// paidOrderForRequest carrega o pedido do parâmetro id, confere o acesso e
// exige que já esteja pago.
func paidOrderForRequest(w http.ResponseWriter, r *http.Request) (*models.Order, bool) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return nil, false
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	if !requireOrderAccess(w, r, order) {
		return nil, false
	}
	if !order.IsPaid() {
		http.Redirect(w, r, "/pedido?id="+strconv.Itoa(order.ID), http.StatusSeeOther)
		return nil, false
	}
	return order, true
}

// OrderReceiptPDF entrega o comprovante para download.
func OrderReceiptPDF(w http.ResponseWriter, r *http.Request) {
	order, ok := paidOrderForRequest(w, r)
	if !ok {
		return
	}
	pdf, err := services.ReceiptPDF(buildReceipt(order))
	if err != nil {
		log.Printf("comprovante do pedido #%d: %v", order.ID, err)
		http.Error(w, "não foi possível gerar o comprovante", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="comprovante-pedido-%d.pdf"`, order.ID))
	// Dados pessoais do comprador: nada de cache compartilhado.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(pdf)
}

// OrderReceiptEmail envia o comprovante para o e-mail do pedido.
func OrderReceiptEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	order, ok := paidOrderForRequest(w, r)
	if !ok {
		return
	}
	volta := "/pedido?id=" + strconv.Itoa(order.ID) + "&email="

	key := "recibo|" + strconv.Itoa(order.ID)
	if allowed, _ := receiptEmailLimiter.Allowed(key); !allowed {
		http.Redirect(w, r, volta+"limite", http.StatusSeeOther)
		return
	}
	receiptEmailLimiter.RegisterFailure(key) // conta todo envio, com ou sem sucesso

	if err := sendReceiptEmail(order); err != nil {
		if errors.Is(err, errSMTPNotConfigured) {
			http.Redirect(w, r, volta+"indisponivel", http.StatusSeeOther)
			return
		}
		log.Printf("e-mail do comprovante do pedido #%d: %v", order.ID, err)
		http.Redirect(w, r, volta+"erro", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, volta+"enviado", http.StatusSeeOther)
}

// sendReceiptOnPayment manda o comprovante assim que o pagamento é confirmado,
// em segundo plano para não atrasar a resposta ao webhook.
func sendReceiptOnPayment(orderID int) {
	if !smtpConfig().Configured() {
		return
	}
	go func() {
		order, err := models.GetOrder(orderID)
		if err != nil {
			log.Printf("e-mail do comprovante do pedido #%d: %v", orderID, err)
			return
		}
		if err := sendReceiptEmail(order); err != nil {
			log.Printf("e-mail do comprovante do pedido #%d: %v", orderID, err)
			return
		}
		log.Printf("comprovante do pedido #%d enviado por e-mail", orderID)
	}()
}

func sendReceiptEmail(order *models.Order) error {
	cfg := smtpConfig()
	if !cfg.Configured() {
		return errSMTPNotConfigured
	}
	receipt := buildReceipt(order)
	pdf, err := services.ReceiptPDF(receipt)
	if err != nil {
		return err
	}
	// Pedido com e-book: o e-mail já leva à biblioteca, onde está o download.
	data := struct {
		services.Receipt
		HasDigital bool
		LibraryURL string
	}{Receipt: receipt, HasDigital: order.HasDigital()}
	if base := strings.TrimRight(models.GetSetting("store_base_url"), "/"); base != "" && data.HasDigital {
		data.LibraryURL = base + "/conta/biblioteca"
	}
	var html bytes.Buffer
	if err := receiptEmailTemplate.Execute(&html, data); err != nil {
		return err
	}
	text := fmt.Sprintf("Olá, %s!\n\nO pagamento do seu pedido nº %d foi confirmado. Total: %s.\n"+
		"O comprovante de compra está em anexo (não é documento fiscal).\n",
		receipt.CustomerName, receipt.OrderID, receipt.Total)
	if data.HasDigital {
		text += "\nSeus livros digitais já estão liberados em Minha biblioteca"
		if data.LibraryURL != "" {
			text += ": " + data.LibraryURL
		}
		text += "\n"
	}
	text += fmt.Sprintf("\nObrigado por comprar no %s.", receipt.StoreName)

	return services.SendEmail(cfg, services.Email{
		To:       order.CustomerEmail,
		Subject:  fmt.Sprintf("Comprovante do pedido nº %d — %s", order.ID, receipt.StoreName),
		TextBody: text,
		HTMLBody: html.String(),
		Attachments: []services.EmailAttachment{{
			Filename:    fmt.Sprintf("comprovante-pedido-%d.pdf", order.ID),
			ContentType: "application/pdf",
			Data:        pdf,
		}},
	})
}

// O html/template escapa os dados do pedido, então um nome de livro ou de
// cliente com HTML não vira código dentro do e-mail.
var receiptEmailTemplate = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="pt-BR"><body style="margin:0;background:#F7EFE2;font-family:Georgia,serif;color:#30261D">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#F7EFE2;padding:32px 12px">
<tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;background:#FCF8F1;border:1px solid #E2CFAE">
  <tr><td style="background:#1F5C3C;padding:28px 32px">
    <p style="margin:0;font-size:28px;font-style:italic;color:#F7EFE2">{{.StoreName}}</p>
    <p style="margin:6px 0 0;font:600 11px Arial,sans-serif;letter-spacing:3px;color:#DDB577">COMPROVANTE DE COMPRA</p>
  </td></tr>
  <tr><td style="padding:28px 32px 8px">
    <p style="margin:0;font-size:22px">Olá, {{.CustomerName}}!</p>
    <p style="margin:12px 0 0;font:15px/1.6 Arial,sans-serif;color:#5A4A3A">
      O pagamento do seu <strong>pedido nº {{.OrderID}}</strong> foi confirmado{{with .PaidAt}} em {{.}}{{end}}.
      O comprovante completo está em anexo, em PDF.
    </p>
  </td></tr>
  {{if .HasDigital}}
  <tr><td style="padding:8px 32px">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>
      <td style="background:#1F5C3C;padding:16px 20px;font:14px/1.6 Arial,sans-serif;color:#F7EFE2">
        <strong>Seus livros digitais já estão liberados.</strong><br>
        {{if .LibraryURL}}<a href="{{.LibraryURL}}" style="color:#DDB577;font-weight:bold">Abrir Minha biblioteca</a> para baixar quando quiser.
        {{else}}Entre na sua conta no site e abra Minha biblioteca para baixar.{{end}}
      </td>
    </tr></table>
  </td></tr>
  {{end}}
  <tr><td style="padding:16px 32px">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font:14px Arial,sans-serif">
      {{range .Items}}
      <tr>
        <td style="padding:8px 0;border-bottom:1px dotted #CFB388">{{.Title}} <span style="color:#93826F">× {{.Quantity}}</span></td>
        <td align="right" style="padding:8px 0;border-bottom:1px dotted #CFB388">{{.Subtotal}}</td>
      </tr>
      {{end}}
      <tr>
        <td style="padding:14px 0 0;color:#75634F">Total</td>
        <td align="right" style="padding:14px 0 0;font:22px Georgia,serif;color:#8A4F2F">{{.Total}}</td>
      </tr>
    </table>
  </td></tr>
  <tr><td style="padding:8px 32px 28px;font:13px/1.6 Arial,sans-serif;color:#75634F">
    Vendido por <strong>{{.SellerName}}</strong>{{with .SellerKind}} ({{.}}){{end}} · Pagamento: {{.PaymentMethod}}<br>
    Entrega em: {{.Address}}
  </td></tr>
  <tr><td style="padding:16px 32px;background:#F7EFE2;font:11px/1.5 Arial,sans-serif;color:#93826F">
    Este comprovante não é documento fiscal e não substitui nota fiscal.
  </td></tr>
</table>
</td></tr></table>
</body></html>`))

// emailTestLimiter: no máximo 5 e-mails de teste por hora.
var emailTestLimiter = newRateLimiter(5, time.Hour, time.Hour)

// emailTestResult é o resultado do botão "Enviar e-mail de teste".
type emailTestResult struct {
	OK       bool
	To       string
	Detail   string // o que o servidor de e-mail respondeu, sem a senha
	Hint     string
	Server   string
	Password int // quantos caracteres a senha salva tem (sem revelá-la)
}

// AdminEmailTest - POST /admin/configuracoes/email-teste: envia um e-mail de
// teste para a própria conta configurada (nunca para outro endereço) e mostra
// o resultado. Serve para conferir a senha de app sem esperar uma compra.
func AdminEmailTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cfg := smtpConfig()
	res := emailTestResult{To: cfg.User, Server: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), Password: len([]rune(cfg.Password))}
	switch {
	case !cfg.Configured():
		res.Detail = "Falta preencher a conta, a senha de app, o servidor ou a porta (e salvar)."
	default:
		if allowed, _ := emailTestLimiter.Allowed("email-teste"); !allowed {
			res.Detail = "Muitos testes seguidos. Tente de novo daqui a pouco."
			break
		}
		emailTestLimiter.RegisterFailure("email-teste")
		err := services.SendEmail(cfg, services.Email{
			To:      cfg.User,
			Subject: "Teste de e-mail do " + cfg.FromName,
			TextBody: "Este é um e-mail de teste enviado pelo painel do " + cfg.FromName + ".\n\n" +
				"Se você está lendo isto, o envio de comprovantes e de boas-vindas está funcionando.",
			HTMLBody: "<p>Este é um e-mail de teste enviado pelo painel do <strong>" + html.EscapeString(cfg.FromName) + "</strong>.</p>" +
				"<p>Se você está lendo isto, o envio de comprovantes e de boas-vindas está funcionando.</p>",
		})
		if err == nil {
			res.OK = true
			res.Detail = "Enviado. Confira a caixa de entrada (e o spam) de " + cfg.User + "."
		} else {
			res.Detail = emailErrorDetail(err, cfg.Password)
			res.Hint = emailErrorHint(err, cfg)
			log.Printf("e-mail de teste: %v", err)
		}
	}
	renderAdminSettings(w, r, false, "", map[string]any{"TesteEmail": res})
}

// emailErrorDetail devolve o erro sem nunca incluir a senha.
func emailErrorDetail(err error, password string) string {
	msg := err.Error()
	if password != "" {
		msg = strings.ReplaceAll(msg, password, "••••")
	}
	return msg
}

// emailErrorHint traduz os erros mais comuns do Gmail em o que fazer.
func emailErrorHint(err error, cfg services.SMTPConfig) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "534") || strings.Contains(msg, "application-specific") || strings.Contains(msg, "5.7.9"):
		return "O Gmail exige uma senha de app (não a senha normal da conta), e a conta precisa ter a verificação em duas etapas ligada."
	case strings.Contains(msg, "535") || strings.Contains(msg, "autenticação") || strings.Contains(msg, "username and password not accepted"):
		if n := len([]rune(cfg.Password)); n != 16 {
			return fmt.Sprintf("A senha salva tem %d caracteres e a senha de app do Google tem 16 letras. Gere uma nova em myaccount.google.com/apppasswords e cole de novo.", n)
		}
		return "O Google recusou a conta ou a senha de app. Confira se o e-mail é o da mesma conta que gerou a senha, gere uma nova senha de app e cole de novo."
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "conexão com o servidor"):
		return "Não foi possível alcançar o servidor de e-mail. Confira o servidor (smtp.gmail.com) e a porta (587)."
	}
	return ""
}
