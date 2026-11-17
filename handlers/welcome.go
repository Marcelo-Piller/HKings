package handlers

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// E-mail de boas-vindas, um para cliente e outro para vendedor. Sai sozinho
// no cadastro, em segundo plano: uma falha no SMTP não pode travar nem
// desfazer o cadastro, e sem SMTP configurado o envio é pulado. Quem já tinha
// conta recebe pelo botão das Configurações (Existente = true). Cada envio
// marca a conta, então ninguém recebe duas vezes.

type welcomeData struct {
	Nome      string
	Loja      string
	Vendedor  bool
	Usuario   string // login do vendedor
	Link      string
	Base      string
	Existente bool // conta criada antes das boas-vindas existirem
}

func sendWelcomeEmail(to string, data welcomeData) {
	to = strings.TrimSpace(to)
	if to == "" || !smtpConfig().Configured() {
		return
	}
	go func() {
		if err := deliverWelcome(to, data); err != nil {
			log.Printf("boas-vindas para %s: %v", to, err)
		}
	}()
}

// deliverWelcome envia na hora e marca a conta como já recebida.
func deliverWelcome(to string, data welcomeData) error {
	cfg := smtpConfig()
	data.Loja = firstNonEmpty(models.GetSetting("store_name"), "Livredo")
	data.Base = firstNonEmpty(strings.TrimRight(models.GetSetting("store_base_url"), "/"), "https://livredo.com.br")
	if data.Vendedor {
		data.Link = data.Base + "/vendedor"
	} else {
		data.Link = data.Base + "/"
	}
	var html bytes.Buffer
	if err := welcomeEmailTemplate.Execute(&html, data); err != nil {
		return err
	}
	subject := "Boas-vindas ao " + data.Loja
	if data.Vendedor {
		subject = "Sua loja no " + data.Loja + ": primeiros passos"
	}
	if err := services.SendEmail(cfg, services.Email{To: to, Subject: subject, TextBody: welcomeText(data), HTMLBody: html.String()}); err != nil {
		return err
	}
	models.MarkWelcomeSent(data.Vendedor, to)
	return nil
}

func welcomeText(d welcomeData) string {
	var b strings.Builder
	b.WriteString("Olá, " + d.Nome + "!\n\n")
	if d.Existente {
		b.WriteString("Obrigado por fazer parte do " + d.Loja + ". Sua conta está ativa.\n\n")
	} else {
		b.WriteString("Seu cadastro no " + d.Loja + " foi concluído com sucesso.\n\n")
	}
	if d.Vendedor {
		b.WriteString("Seu usuário de vendedor é: " + d.Usuario + "\n\nPrimeiros passos:\n" +
			"1. Conecte sua conta do Mercado Pago no painel: é para lá que vai o dinheiro das vendas.\n" +
			"2. Cadastre seus livros, um a um (o ISBN preenche os dados) ou pela importação de planilha.\n" +
			"3. Informe o endereço de despacho para o frete ser calculado.\n" +
			"4. Autor ou editora? Peça a aprovação para vender e-books e escreva a página do autor.\n")
	} else {
		b.WriteString("No " + d.Loja + " você encontra livros novos e usados de leitores, sebos, livrarias e autores independentes, além de e-books com amostra grátis.\n" +
			"Seus pedidos e livros digitais ficam guardados em Minha conta.\n")
	}
	b.WriteString("\nAcesse: " + d.Link + "\n\nSe você não reconhece esta conta, ignore este e-mail.")
	return b.String()
}

// ── Envio para quem já estava cadastrado ─────────────────────────────────────

var welcomeBatch struct {
	sync.Mutex
	Running      bool
	Sent, Failed int
	LastError    string
	Finished     string
}

// welcomeBatchStatus é o que a página de Configurações mostra.
type welcomeBatchStatus struct {
	Running              bool
	Sent, Failed         int
	LastError, Finished  string
	Clientes, Vendedores int
}

func currentWelcomeBatch() welcomeBatchStatus {
	welcomeBatch.Lock()
	defer welcomeBatch.Unlock()
	c, v := models.CountPendingWelcomes()
	return welcomeBatchStatus{Running: welcomeBatch.Running, Sent: welcomeBatch.Sent, Failed: welcomeBatch.Failed,
		LastError: welcomeBatch.LastError, Finished: welcomeBatch.Finished, Clientes: c, Vendedores: v}
}

// AdminWelcomeBatch - POST /admin/configuracoes/boas-vindas: envia as
// boas-vindas a todos os clientes e vendedores que ainda não receberam.
func AdminWelcomeBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !smtpConfig().Configured() {
		renderAdminSettings(w, r, false, "Configure o e-mail (conta e senha de app) antes de enviar as boas-vindas.")
		return
	}
	welcomeBatch.Lock()
	if !welcomeBatch.Running {
		welcomeBatch.Running, welcomeBatch.Sent, welcomeBatch.Failed, welcomeBatch.LastError, welcomeBatch.Finished = true, 0, 0, "", ""
		go runWelcomeBatch()
	}
	welcomeBatch.Unlock()
	http.Redirect(w, r, "/admin/configuracoes#boasvindas", http.StatusSeeOther)
}

// welcomeBatchDelay espaça os envios: o Gmail barra contas que disparam
// muitas mensagens de uma vez (limite de cerca de 500 por dia).
var welcomeBatchDelay = 3 * time.Second

func runWelcomeBatch() {
	defer func() {
		welcomeBatch.Lock()
		welcomeBatch.Running = false
		welcomeBatch.Finished = time.Now().In(fusoBrasilia).Format("02/01 às 15:04")
		welcomeBatch.Unlock()
	}()
	targets, err := models.PendingWelcomes()
	if err != nil {
		log.Printf("boas-vindas em lote: %v", err)
		return
	}
	failuresInARow := 0
	for i, t := range targets {
		if i >= 450 {
			// O resto vai num próximo clique, amanhã: fica abaixo do limite diário do Gmail.
			break
		}
		err := deliverWelcome(t.Email, welcomeData{Nome: t.Name, Vendedor: t.Seller, Usuario: t.Username, Existente: true})
		welcomeBatch.Lock()
		if err != nil {
			welcomeBatch.Failed++
			welcomeBatch.LastError = emailErrorDetail(err, smtpConfig().Password)
			failuresInARow++
		} else {
			welcomeBatch.Sent++
			failuresInARow = 0
		}
		welcomeBatch.Unlock()
		if failuresInARow >= 3 {
			log.Printf("boas-vindas em lote: parado depois de 3 falhas seguidas: %v", err)
			return
		}
		time.Sleep(welcomeBatchDelay)
	}
	log.Printf("boas-vindas em lote: %d enviados, %d falharam", welcomeBatch.Sent, welcomeBatch.Failed)
}

// html/template escapa nome e usuário: ninguém injeta HTML pelo cadastro.
var welcomeEmailTemplate = template.Must(template.New("boasvindas").Parse(`<!DOCTYPE html>
<html lang="pt-BR"><body style="margin:0;background:#F7EFE2;font-family:Georgia,serif;color:#30261D">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#F7EFE2;padding:32px 12px"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;background:#FCF8F1;border:1px solid #E2CFAE">
  <tr><td style="background:#1F5C3C;padding:28px 32px">
    <p style="margin:0;font-size:28px;font-style:italic;color:#F7EFE2">{{.Loja}}</p>
    <p style="margin:6px 0 0;font:600 11px Arial,sans-serif;letter-spacing:3px;color:#DDB577">{{if .Vendedor}}ÁREA DO VENDEDOR{{else if .Existente}}BOAS-VINDAS{{else}}CADASTRO CONCLUÍDO{{end}}</p>
  </td></tr>
  <tr><td style="padding:28px 32px;font:15px/1.6 Arial,sans-serif;color:#5A4A3A">
    <p style="margin:0;font:22px Georgia,serif;color:#30261D">Olá, {{.Nome}}!</p>
    {{if .Existente}}
    <p style="margin:12px 0 0">Obrigado por fazer parte do <strong>{{.Loja}}</strong>{{if .Vendedor}} como vendedor{{end}}. Sua conta está ativa.</p>
    {{else}}
    <p style="margin:12px 0 0">Seu cadastro {{if .Vendedor}}de vendedor {{end}}no <strong>{{.Loja}}</strong> foi concluído com sucesso.</p>
    {{end}}
    {{if .Vendedor}}
    <p style="margin:12px 0 0">Seu usuário é <strong>{{.Usuario}}</strong>. Para começar a vender:</p>
    <ol style="margin:8px 0 0;padding-left:20px">
      <li style="margin-top:6px"><strong>Conecte o Mercado Pago</strong> no painel: é para lá que vai o dinheiro das vendas, e sem isso seus livros não aparecem na loja.</li>
      <li style="margin-top:6px"><strong>Cadastre seus livros</strong>: um a um (o ISBN preenche título, autor e capa) ou o estoque inteiro pela <strong>importação de planilha</strong>.</li>
      <li style="margin-top:6px"><strong>Informe o endereço de despacho</strong> para o frete ser calculado na compra.</li>
      <li style="margin-top:6px">É autor ou editora? Peça a aprovação para <strong>vender e-books</strong> e escreva a <strong>página do autor</strong>.</li>
    </ol>
    {{else}}
    <p style="margin:12px 0 0">Aqui você encontra livros novos e usados de leitores, sebos, livrarias e autores independentes, além de e-books com <strong>amostra grátis</strong> para ler antes de comprar.</p>
    <p style="margin:12px 0 0">Seus pedidos e livros digitais ficam guardados em <strong>Minha conta</strong>.</p>
    {{end}}
    <p style="margin:24px 0 0"><a href="{{.Link}}" style="background:#1F5C3C;color:#FCF8F1;padding:12px 24px;border-radius:999px;text-decoration:none;font-weight:bold">{{if .Vendedor}}Abrir meu painel{{else}}Ir para a loja{{end}}</a></p>
  </td></tr>
  <tr><td style="padding:16px 32px;background:#F7EFE2;font:11px/1.5 Arial,sans-serif;color:#93826F">
    Você recebeu esta mensagem porque tem uma conta no {{.Loja}}. Se não reconhece esta conta, ignore este e-mail.
    <a href="{{.Base}}/privacidade" style="color:#93826F">Política de Privacidade</a>
  </td></tr>
</table></td></tr></table></body></html>`))
