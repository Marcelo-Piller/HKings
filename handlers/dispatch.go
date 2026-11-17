package handlers

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Despacho: avisos ao comprador a cada passo (pronto para envio, postado,
// entregue), com textos editáveis em Configurações; lembretes e alertas de
// atraso ao vendedor; rastreio automático das etiquetas do Melhor Envio; e
// pausa das lojas que atrasam (models/dispatch.go).

// buyerNotice é um modelo de aviso ao comprador.
type buyerNotice struct {
	Key, Label     string
	Subject, Body  string // padrão
	SubjKey, BodyK string // chaves em settings
}

var buyerNotices = []buyerNotice{
	{Key: models.StatusReady, Label: "Pronto para envio",
		Subject: "Seu pedido nº {pedido} está pronto para envio",
		Body: "Olá, {nome}!\n\nBoas notícias: {vendedor} separou e embalou seu pedido nº {pedido}. " +
			"Ele vai para os Correios ou para a transportadora até {prazo}.\n\nAcompanhe em: {link_pedido}"},
	{Key: "enviado", Label: "Postado",
		Subject: "Seu pedido nº {pedido} foi postado",
		Body: "Olá, {nome}!\n\nSeu pedido nº {pedido} foi postado por {vendedor}.\n" +
			"Código de rastreio: {rastreio}\nAcompanhe a entrega: {link_rastreio}\n\nDetalhes do pedido: {link_pedido}"},
	{Key: "entregue", Label: "Entregue",
		Subject: "Seu pedido nº {pedido} foi entregue",
		Body: "Olá, {nome}!\n\nSeu pedido nº {pedido} foi entregue. Boa leitura!\n\n" +
			"Que tal avaliar o vendedor? Sua opinião ajuda outros leitores: {link_pedido}"},
}

func init() {
	for i := range buyerNotices {
		buyerNotices[i].SubjKey = "msg_" + buyerNotices[i].Key + "_assunto"
		buyerNotices[i].BodyK = "msg_" + buyerNotices[i].Key + "_texto"
	}
}

func noticeFor(status string) *buyerNotice {
	for i := range buyerNotices {
		if buyerNotices[i].Key == status {
			return &buyerNotices[i]
		}
	}
	return nil
}

// noticeText devolve assunto e texto configurados (ou os padrões).
func noticeText(n *buyerNotice) (string, string) {
	return firstNonEmpty(models.GetSetting(n.SubjKey), n.Subject), firstNonEmpty(models.GetSetting(n.BodyK), n.Body)
}

// fillNotice troca os marcadores {nome}, {pedido} etc. pelos dados do pedido.
func fillNotice(text string, o *models.Order, d models.Dispatch) string {
	base := firstNonEmpty(strings.TrimRight(models.GetSetting("store_base_url"), "/"), "https://livredo.com.br")
	vendedor := "o Livredo"
	if o.SellerID != 0 {
		if s, err := models.GetSeller(o.SellerID); err == nil {
			vendedor = s.Name
		}
	}
	rastreio := firstNonEmpty(o.TrackingCode, "(ainda não informado)")
	return strings.NewReplacer(
		"{nome}", strings.Fields(o.CustomerName + " ")[0],
		"{pedido}", strconv.Itoa(o.ID),
		"{loja}", firstNonEmpty(models.GetSetting("store_name"), "Livredo"),
		"{vendedor}", vendedor,
		"{prazo}", firstNonEmpty(d.DeadlineLabel(), "o prazo combinado"),
		"{rastreio}", rastreio,
		"{link_rastreio}", firstNonEmpty(o.TrackingURL(), base+"/pedido?id="+strconv.Itoa(o.ID)),
		"{link_pedido}", base+"/pedido?id="+strconv.Itoa(o.ID),
	).Replace(text)
}

// notifyBuyer avisa o comprador da mudança de situação, uma vez por passo.
func notifyBuyer(orderID int, status string) {
	n := noticeFor(status)
	if n == nil || !smtpConfig().Configured() || !models.ClaimDispatchNotice(orderID, "comprador_"+status) {
		return
	}
	go func() {
		o, err := models.GetOrder(orderID)
		if err != nil || o.CustomerEmail == "" {
			return
		}
		d := models.GetDispatch(orderID)
		subject, body := noticeText(n)
		subject, body = fillNotice(subject, o, d), fillNotice(body, o, d)
		if err := services.SendEmail(smtpConfig(), services.Email{
			To: o.CustomerEmail, Subject: strings.ReplaceAll(subject, "\n", " "), TextBody: body, HTMLBody: textToHTML(body),
		}); err != nil {
			log.Printf("aviso %s do pedido #%d: %v", status, orderID, err)
		}
	}()
}

// textToHTML transforma o texto do aviso em HTML simples, com links clicáveis.
func textToHTML(text string) string {
	var b strings.Builder
	b.WriteString(`<div style="font:15px/1.6 Arial,sans-serif;color:#30261D;max-width:560px">`)
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		b.WriteString(`<p style="margin:0 0 14px">`)
		for i, line := range strings.Split(para, "\n") {
			if i > 0 {
				b.WriteString("<br>")
			}
			for j, word := range strings.Split(line, " ") {
				if j > 0 {
					b.WriteString(" ")
				}
				if strings.HasPrefix(word, "https://") || strings.HasPrefix(word, "http://") {
					fmt.Fprintf(&b, `<a href="%s" style="color:#1F5C3C">%s</a>`, html.EscapeString(word), html.EscapeString(word))
				} else {
					b.WriteString(html.EscapeString(word))
				}
			}
		}
		b.WriteString("</p>")
	}
	b.WriteString("</div>")
	return b.String()
}

// sendSellerMail avisa o vendedor (ou o admin, em venda do próprio site).
func sendSellerMail(sellerID int, subject, body string) {
	if !smtpConfig().Configured() {
		return
	}
	to := smtpConfig().User
	if sellerID != 0 {
		s, err := models.GetSeller(sellerID)
		if err != nil || s.Email == "" {
			return
		}
		to = s.Email
	}
	if err := services.SendEmail(smtpConfig(), services.Email{To: to, Subject: subject, TextBody: body, HTMLBody: textToHTML(body)}); err != nil {
		log.Printf("aviso ao vendedor %d: %v", sellerID, err)
	}
}

// ── Rotina automática ────────────────────────────────────────────────────────

// StartDispatchJobs roda a cada 30 minutos: lembretes, atrasos, pausas e
// rastreio.
func StartDispatchJobs() {
	go func() {
		time.Sleep(time.Minute)
		for {
			runDispatchJobs()
			time.Sleep(30 * time.Minute)
		}
	}()
}

func runDispatchJobs() {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("rotina de despacho: %v", p)
		}
	}()
	base := firstNonEmpty(strings.TrimRight(models.GetSetting("store_base_url"), "/"), "https://livredo.com.br")
	painel := func(sellerID, orderID int) string {
		if sellerID == 0 {
			return fmt.Sprintf("%s/admin/pedidos/detalhe?id=%d", base, orderID)
		}
		return fmt.Sprintf("%s/vendedor/pedidos/detalhe?id=%d", base, orderID)
	}
	// Lembrete: faltam menos de 12 horas para o prazo de postagem.
	for _, o := range models.DueSoonOrders(12 * time.Hour) {
		if models.ClaimDispatchNotice(o.OrderID, "lembrete") {
			sendSellerMail(o.SellerID, fmt.Sprintf("Lembrete: poste o pedido nº %d até %s", o.OrderID, o.DeadlineLabel()),
				fmt.Sprintf("Olá!\n\nO prazo para postar o pedido nº %d termina em %s. Envios no prazo mantêm seus livros em destaque na loja; "+
					"atrasos reduzem a exposição e podem pausar a loja.\n\nAbra o pedido: %s", o.OrderID, o.DeadlineLabel(), painel(o.SellerID, o.OrderID)))
		}
	}
	// Atraso: prazo vencido sem postagem.
	sellers := map[int]bool{}
	for _, o := range models.ListOverdueOrders() {
		sellers[o.SellerID] = true
		if models.ClaimDispatchNotice(o.OrderID, "atraso") {
			sendSellerMail(o.SellerID, fmt.Sprintf("Pedido nº %d atrasado: poste hoje", o.OrderID),
				fmt.Sprintf("Olá!\n\nO prazo para postar o pedido nº %d venceu em %s. O comprador está esperando.\n\n"+
					"Poste hoje e marque como Postado no painel. Pedidos atrasados há mais de 3 dias úteis pausam a loja.\n\nAbra o pedido: %s",
					o.OrderID, o.DeadlineLabel(), painel(o.SellerID, o.OrderID)))
		}
	}
	// Pausa automática de quem atrasa demais.
	for _, id := range models.SellersWithShipments(60) {
		sellers[id] = true
	}
	for id := range sellers {
		if id == 0 {
			continue
		}
		s := models.GetShippingStats(id)
		if !s.ShouldPause() {
			continue
		}
		reason := fmt.Sprintf("%d%% dos envios atrasados nos últimos 60 dias", 100-s.OnTimePercent())
		if s.WorstOverdue > 3 {
			reason = fmt.Sprintf("pedido sem postagem há %d dias úteis depois do prazo", s.WorstOverdue)
		}
		if paused, _ := models.PauseSeller(id, reason); paused {
			log.Printf("despacho: loja do vendedor %d pausada (%s)", id, reason)
			sendSellerMail(id, "Sua loja no Livredo foi pausada por atraso nos envios",
				"Olá!\n\nSeus livros saíram da loja por enquanto: "+reason+".\n\n"+
					"Continue despachando os pedidos pelo painel. Para voltar a vender, poste os pedidos pendentes e fale com o Livredo "+
					"respondendo este e-mail.\n\nPainel: "+base+"/vendedor")
		}
	}
	models.ForgetShippingBadges()
	syncTracking()
}

// syncTracking atualiza os pedidos com etiqueta do Melhor Envio: postado e
// entregue saem do rastreio, sem o vendedor precisar clicar.
func syncTracking() {
	for _, t := range models.ListTrackableOrders() {
		api, err := meAPIFor(t.SellerID)
		if err != nil {
			continue
		}
		tr, err := api.TrackingStatus(t.MEOrderID)
		if err != nil {
			log.Printf("rastreio do pedido #%d: %v", t.OrderID, err)
			continue
		}
		models.SetTrackStatus(t.OrderID, tr.Status)
		if tr.Code != "" {
			models.SetOrderTracking(t.OrderID, tr.Code)
		}
		if !tr.PostedAt.IsZero() && (t.Status == "pago" || t.Status == models.StatusReady) {
			if models.MarkOrderPosted(t.OrderID, tr.PostedAt) == nil {
				notifyBuyer(t.OrderID, "enviado")
			}
		}
		if tr.Status == "delivered" || !tr.DeliveredAt.IsZero() {
			if models.MarkOrderDelivered(t.OrderID, tr.DeliveredAt) == nil {
				notifyBuyer(t.OrderID, "entregue")
			}
		}
	}
}

// ── Painel do admin ──────────────────────────────────────────────────────────

// AdminReactivateSeller - POST /admin/vendedores/reativar: devolve os livros
// de uma loja pausada à vitrine e zera o histórico de atrasos.
func AdminReactivateSeller(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	id, _ := strconv.Atoi(r.FormValue("id"))
	if err := models.ReactivateSeller(id); err != nil {
		log.Printf("reativar vendedor %d: %v", id, err)
	}
	models.ForgetShippingBadges()
	http.Redirect(w, r, "/admin#envios", http.StatusSeeOther)
}
