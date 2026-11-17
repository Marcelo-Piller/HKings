package models

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vendas/loja/database"
)

// Prazo de despacho (SLA de postagem), medalha "Envio Rápido" e penalidades.
//
// Quando o pagamento é aprovado, o pedido ganha um prazo para ser postado,
// contado em dias úteis: lojas (editora, sebo, livraria) têm 2 dias úteis
// (48 h) e pessoas (autor independente, leitor) têm 3 (72 h). Os valores
// ficam em Configurações. O vendedor avisa "Pronto para envio" e "Postado";
// o comprador recebe e-mail a cada passo.
//
// Desempenho dos últimos 60 dias:
//   - Envio Rápido: 80% ou mais das postagens no mesmo dia útil do pagamento
//     (mínimo de 3 envios). A medalha aparece nos anúncios.
//   - Exposição reduzida: 25% ou mais de envios atrasados (mínimo de 4) ou
//     algum pedido atrasado agora. Os livros descem nas listas da loja.
//   - Loja pausada: pedido atrasado há mais de 3 dias úteis, ou 50% ou mais
//     de envios atrasados (mínimo de 6). Os livros saem da loja até o admin
//     reativar; a conta continua entrando para despachar os pedidos.

// Situações novas do pedido físico, entre "pago" e "enviado".
const StatusReady = "pronto"

var fusoBR = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*60*60)
}()

// ── Dias úteis ───────────────────────────────────────────────────────────────

// easter calcula o domingo de Páscoa (algoritmo de Meeus/Butcher).
func easter(year int) time.Time {
	a, b, c := year%19, year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, fusoBR)
}

// IsBusinessDay diz se os Correios trabalham no dia: sem sábados, domingos,
// feriados nacionais, Carnaval e Sexta-feira Santa.
func IsBusinessDay(t time.Time) bool {
	t = t.In(fusoBR)
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	md := t.Format("01-02")
	switch md {
	case "01-01", "04-21", "05-01", "09-07", "10-12", "11-02", "11-15", "11-20", "12-25":
		return false
	}
	e := easter(t.Year())
	for _, off := range []int{-48, -47, -2} { // Carnaval (seg e ter) e Sexta-feira Santa
		if e.AddDate(0, 0, off).Format("01-02") == md {
			return false
		}
	}
	return true
}

// AddBusinessDays soma dias úteis: pago na sexta às 15 h com 2 dias úteis,
// o prazo é terça às 15 h.
func AddBusinessDays(t time.Time, n int) time.Time {
	t = t.In(fusoBR)
	for n > 0 {
		t = t.Add(24 * time.Hour)
		if IsBusinessDay(t) {
			n--
		}
	}
	return t
}

// endOfFirstBusinessDay é o fim do dia útil do pagamento (ou do próximo dia
// útil, se pago num sábado, domingo ou feriado).
func endOfFirstBusinessDay(t time.Time) time.Time {
	t = t.In(fusoBR)
	for !IsBusinessDay(t) {
		t = t.AddDate(0, 0, 1)
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, 0, fusoBR)
}

// BusinessDaysLate conta os dias úteis entre o prazo e agora.
func BusinessDaysLate(deadline, now time.Time) int {
	n := 0
	for t := deadline.In(fusoBR).Add(24 * time.Hour); !t.After(now); t = t.Add(24 * time.Hour) {
		if IsBusinessDay(t) {
			n++
		}
	}
	return n
}

// ── Prazo do pedido ──────────────────────────────────────────────────────────

// SLAHours devolve o prazo de postagem do vendedor, em horas úteis.
func SLAHours(sellerID int) int {
	setting := func(key string, def int) int {
		if n, err := strconv.Atoi(strings.TrimSpace(GetSetting(key))); err == nil && n >= 24 && n <= 240 {
			return n
		}
		return def
	}
	loja, pessoa := setting("sla_loja_horas", 48), setting("sla_pessoa_horas", 72)
	if sellerID == 0 {
		return loja
	}
	var profile, document string
	database.DB.QueryRow(`SELECT seller_profile, document FROM sellers WHERE id=?`, sellerID).Scan(&profile, &document)
	switch profile {
	case "editora", "sebo", "livraria":
		return loja
	case "autor", "leitor":
		return pessoa
	}
	if len(document) == 14 { // CNPJ sem tipo informado: loja
		return loja
	}
	return pessoa
}

// SLALabel descreve o prazo: "48 h úteis (2 dias úteis)".
func SLALabel(hours int) string {
	return fmt.Sprintf("%d h úteis (%d dias úteis)", hours, hours/24)
}

func parseDBTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func dbTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// SetShipDeadline grava o prazo de postagem de um pedido recém-pago com
// livro físico. Chamado na confirmação do pagamento.
func SetShipDeadline(orderID int) error {
	var paidAt, status string
	var sellerID int
	if err := database.DB.QueryRow(`SELECT COALESCE(paid_at,''), status, seller_id FROM orders WHERE id=?`, orderID).Scan(&paidAt, &status, &sellerID); err != nil {
		return err
	}
	if status != "pago" {
		return nil // pedido só de e-books já nasce entregue
	}
	paid, ok := parseDBTime(paidAt)
	if !ok {
		paid = time.Now()
	}
	deadline := AddBusinessDays(paid, SLAHours(sellerID)/24)
	_, err := database.DB.Exec(`UPDATE orders SET ship_deadline=? WHERE id=? AND ship_deadline=''`, dbTime(deadline), orderID)
	return err
}

// Dispatch é o andamento do despacho de um pedido.
type Dispatch struct {
	Deadline    time.Time
	ReadyAt     time.Time
	PostedAt    time.Time
	DeliveredAt time.Time
	Late        bool   // postado depois do prazo
	TrackStatus string // último status do rastreio do Melhor Envio
}

func (d Dispatch) HasDeadline() bool { return !d.Deadline.IsZero() }

// Overdue diz se o prazo passou sem postagem.
func (d Dispatch) Overdue() bool {
	return d.HasDeadline() && d.PostedAt.IsZero() && time.Now().After(d.Deadline)
}

// DeadlineLabel: "até 08/10 às 15:04".
func (d Dispatch) DeadlineLabel() string {
	if d.Deadline.IsZero() {
		return ""
	}
	return d.Deadline.In(fusoBR).Format("02/01 às 15:04")
}

func fmtBR(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(fusoBR).Format("02/01 às 15:04")
}

func (d Dispatch) ReadyLabel() string     { return fmtBR(d.ReadyAt) }
func (d Dispatch) PostedLabel() string    { return fmtBR(d.PostedAt) }
func (d Dispatch) DeliveredLabel() string { return fmtBR(d.DeliveredAt) }

// TimeLeftLabel é o tempo até o prazo: "faltam 5 h", "atrasado há 2 dias úteis".
func (d Dispatch) TimeLeftLabel() string {
	if !d.HasDeadline() || !d.PostedAt.IsZero() {
		return ""
	}
	now := time.Now()
	if now.After(d.Deadline) {
		if n := BusinessDaysLate(d.Deadline, now); n > 0 {
			return fmt.Sprintf("atrasado há %d dia(s) útil(eis)", n)
		}
		return "prazo vencido"
	}
	left := d.Deadline.Sub(now)
	if left < 24*time.Hour {
		return fmt.Sprintf("faltam %d h", int(left.Hours())+1)
	}
	return "até " + d.DeadlineLabel()
}

// GetDispatch lê o andamento do despacho do pedido.
func GetDispatch(orderID int) Dispatch {
	var d Dispatch
	var deadline, ready, posted, delivered string
	var late int
	database.DB.QueryRow(`SELECT ship_deadline, ready_at, posted_at, delivered_at, late, track_status FROM orders WHERE id=?`, orderID).
		Scan(&deadline, &ready, &posted, &delivered, &late, &d.TrackStatus)
	d.Deadline, _ = parseDBTime(deadline)
	d.ReadyAt, _ = parseDBTime(ready)
	d.PostedAt, _ = parseDBTime(posted)
	d.DeliveredAt, _ = parseDBTime(delivered)
	d.Late = late == 1
	return d
}

// ErrDispatchStep: o passo do despacho não vale para a situação do pedido.
var ErrDispatchStep = errors.New("este passo não vale para a situação atual do pedido")

// MarkOrderReady: o vendedor separou e embalou o livro ("Pronto para envio").
func MarkOrderReady(orderID int) error {
	res, err := database.DB.Exec(`UPDATE orders SET status='pronto', ready_at=CURRENT_TIMESTAMP WHERE id=? AND status='pago'`, orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrDispatchStep
	}
	return nil
}

// MarkOrderPosted: o livro foi entregue aos Correios ou à transportadora.
// Marca atraso se passou do prazo.
func MarkOrderPosted(orderID int, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	res, err := database.DB.Exec(`UPDATE orders SET status='enviado', posted_at=?,
			late = CASE WHEN ship_deadline <> '' AND ? > ship_deadline THEN 1 ELSE 0 END
		WHERE id=? AND status IN ('pago','pronto')`, dbTime(at), dbTime(at), orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrDispatchStep
	}
	return nil
}

// MarkOrderDelivered: entregue ao comprador.
func MarkOrderDelivered(orderID int, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	res, err := database.DB.Exec(`UPDATE orders SET status='entregue', delivered_at=? WHERE id=? AND status IN ('pronto','enviado')`, dbTime(at), orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrDispatchStep
	}
	return nil
}

// SetTrackStatus guarda o último status do rastreio.
func SetTrackStatus(orderID int, status string) {
	database.DB.Exec(`UPDATE orders SET track_status=?, track_checked_at=CURRENT_TIMESTAMP WHERE id=?`, status, orderID)
}

// ── Desempenho do vendedor ───────────────────────────────────────────────────

// ShippingStats é o desempenho de envio do vendedor nos últimos 60 dias.
type ShippingStats struct {
	Shipped      int // postados
	Late         int // postados depois do prazo
	SameDay      int // postados no mesmo dia útil do pagamento
	OverdueNow   int // prazo vencido sem postagem
	WorstOverdue int // dias úteis do atraso mais antigo
}

// LateRate é a fração de envios atrasados.
func (s ShippingStats) LateRate() float64 {
	if s.Shipped == 0 {
		return 0
	}
	return float64(s.Late) / float64(s.Shipped)
}

// OnTimePercent é o percentual de envios no prazo.
func (s ShippingStats) OnTimePercent() int {
	if s.Shipped == 0 {
		return 100
	}
	return int(100 - s.LateRate()*100 + 0.5)
}

// FastShipper: medalha "Envio Rápido".
func (s ShippingStats) FastShipper() bool {
	return s.Shipped >= 3 && float64(s.SameDay)/float64(s.Shipped) >= 0.8 && s.OverdueNow == 0
}

// ReducedExposure: livros descem nas listas.
func (s ShippingStats) ReducedExposure() bool {
	return s.OverdueNow > 0 || (s.Shipped >= 4 && s.LateRate() >= 0.25)
}

// ShouldPause: livros saem da loja.
func (s ShippingStats) ShouldPause() bool {
	return s.WorstOverdue > 3 || (s.Shipped >= 6 && s.LateRate() >= 0.5)
}

// GetShippingStats calcula o desempenho de envio. Pedidos pagos antes da
// última reativação pelo admin não contam (o vendedor recomeça do zero).
func GetShippingStats(sellerID int) ShippingStats {
	var s ShippingStats
	var reset string
	database.DB.QueryRow(`SELECT COALESCE(penalty_reset_at,'') FROM sellers WHERE id=?`, sellerID).Scan(&reset)
	since := dbTime(time.Now().AddDate(0, 0, -60))
	if reset > since {
		since = reset
	}
	rows, err := database.DB.Query(`SELECT COALESCE(paid_at,''), ship_deadline, posted_at, late FROM orders
		WHERE seller_id=? AND ship_deadline <> '' AND paid_at >= ? AND status IN ('pago','pronto','enviado','entregue')`, sellerID, since)
	if err != nil {
		return s
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var paidAt, deadline, posted string
		var late int
		if rows.Scan(&paidAt, &deadline, &posted, &late) != nil {
			continue
		}
		dl, _ := parseDBTime(deadline)
		if posted == "" {
			if now.After(dl) {
				s.OverdueNow++
				if n := BusinessDaysLate(dl, now); n > s.WorstOverdue {
					s.WorstOverdue = n
				}
			}
			continue
		}
		s.Shipped++
		if late == 1 {
			s.Late++
		}
		paid, ok1 := parseDBTime(paidAt)
		post, ok2 := parseDBTime(posted)
		if ok1 && ok2 && !post.After(endOfFirstBusinessDay(paid)) {
			s.SameDay++
		}
	}
	return s
}

// SellerBadges são as marcas de envio dos vendedores, para a vitrine.
type SellerBadges struct {
	FastShipper     bool
	ReducedExposure bool
}

// badgeCache guarda as marcas por 5 minutos: a vitrine é aberta muito mais
// vezes do que o desempenho de envio muda.
var badgeCache = struct {
	sync.Mutex
	m map[int]cachedBadge
}{m: map[int]cachedBadge{}}

type cachedBadge struct {
	b  SellerBadges
	at time.Time
}

// ShippingBadges calcula as marcas de envio de vários vendedores de uma vez.
func ShippingBadges(sellerIDs []int) map[int]SellerBadges {
	out := map[int]SellerBadges{}
	badgeCache.Lock()
	defer badgeCache.Unlock()
	for _, id := range sellerIDs {
		if _, ok := out[id]; ok || id == 0 {
			continue
		}
		if c, ok := badgeCache.m[id]; ok && time.Since(c.at) < 5*time.Minute {
			out[id] = c.b
			continue
		}
		s := GetShippingStats(id)
		b := SellerBadges{FastShipper: s.FastShipper(), ReducedExposure: s.ReducedExposure()}
		badgeCache.m[id] = cachedBadge{b, time.Now()}
		out[id] = b
	}
	return out
}

// ForgetShippingBadges descarta as marcas guardadas (depois de mudanças).
func ForgetShippingBadges() {
	badgeCache.Lock()
	badgeCache.m = map[int]cachedBadge{}
	badgeCache.Unlock()
}

// ── Pausa por atraso ─────────────────────────────────────────────────────────

// SellerPause é a situação de pausa da loja.
type SellerPause struct {
	Paused bool
	Reason string
	Since  string
}

func GetSellerPause(sellerID int) SellerPause {
	var p SellerPause
	var paused int
	database.DB.QueryRow(`SELECT listing_paused, paused_reason, COALESCE(paused_at,'') FROM sellers WHERE id=?`, sellerID).Scan(&paused, &p.Reason, &p.Since)
	p.Paused = paused == 1
	return p
}

// PauseSeller tira os livros do vendedor da loja. Devolve true se pausou agora.
func PauseSeller(sellerID int, reason string) (bool, error) {
	res, err := database.DB.Exec(`UPDATE sellers SET listing_paused=1, paused_reason=?, paused_at=CURRENT_TIMESTAMP
		WHERE id=? AND listing_paused=0`, reason, sellerID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReactivateSeller devolve os livros à loja e zera o histórico de atrasos
// (só pedidos pagos daqui em diante contam).
func ReactivateSeller(sellerID int) error {
	_, err := database.DB.Exec(`UPDATE sellers SET listing_paused=0, paused_reason='', penalty_reset_at=CURRENT_TIMESTAMP WHERE id=?`, sellerID)
	return err
}

// PausedSeller é uma linha da lista do admin.
type PausedSeller struct {
	ID     int
	Name   string
	Reason string
	Since  string
}

// ListPausedSellers lista as lojas pausadas por atraso.
func ListPausedSellers() []PausedSeller {
	rows, err := database.DB.Query(`SELECT id, name, paused_reason, COALESCE(paused_at,'') FROM sellers WHERE listing_paused=1 ORDER BY paused_at`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []PausedSeller
	for rows.Next() {
		var p PausedSeller
		if rows.Scan(&p.ID, &p.Name, &p.Reason, &p.Since) == nil {
			out = append(out, p)
		}
	}
	return out
}

// OverdueOrder é um pedido com prazo de postagem vencido.
type OverdueOrder struct {
	OrderID    int
	SellerID   int
	SellerName string
	SellerMail string
	Deadline   time.Time
	DaysLate   int
}

func (o OverdueOrder) DeadlineLabel() string { return fmtBR(o.Deadline) }

// ListOverdueOrders lista os pedidos pagos com o prazo de postagem vencido.
func ListOverdueOrders() []OverdueOrder {
	rows, err := database.DB.Query(`SELECT o.id, o.seller_id, COALESCE(s.name,'Livredo'), COALESCE(s.email,''), o.ship_deadline
		FROM orders o LEFT JOIN sellers s ON s.id = o.seller_id
		WHERE o.status IN ('pago','pronto') AND o.ship_deadline <> '' AND o.ship_deadline < ? ORDER BY o.ship_deadline`, dbTime(time.Now()))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []OverdueOrder
	for rows.Next() {
		var o OverdueOrder
		var dl string
		if rows.Scan(&o.OrderID, &o.SellerID, &o.SellerName, &o.SellerMail, &dl) == nil {
			o.Deadline, _ = parseDBTime(dl)
			o.DaysLate = BusinessDaysLate(o.Deadline, time.Now())
			out = append(out, o)
		}
	}
	return out
}

// ── Avisos já enviados (para não repetir) ────────────────────────────────────

// ClaimDispatchNotice marca que o aviso do tipo (lembrete, atraso…) foi
// enviado para o pedido. Devolve false se já tinha sido.
func ClaimDispatchNotice(orderID int, kind string) bool {
	res, err := database.DB.Exec(`INSERT OR IGNORE INTO dispatch_notices (order_id, kind) VALUES (?,?)`, orderID, kind)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// DueSoonOrders lista pedidos ainda não postados com prazo nas próximas horas.
func DueSoonOrders(within time.Duration) []OverdueOrder {
	rows, err := database.DB.Query(`SELECT o.id, o.seller_id, COALESCE(s.name,'Livredo'), COALESCE(s.email,''), o.ship_deadline
		FROM orders o LEFT JOIN sellers s ON s.id = o.seller_id
		WHERE o.status IN ('pago','pronto') AND o.ship_deadline <> '' AND o.ship_deadline BETWEEN ? AND ?`,
		dbTime(time.Now()), dbTime(time.Now().Add(within)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []OverdueOrder
	for rows.Next() {
		var o OverdueOrder
		var dl string
		if rows.Scan(&o.OrderID, &o.SellerID, &o.SellerName, &o.SellerMail, &dl) == nil {
			o.Deadline, _ = parseDBTime(dl)
			out = append(out, o)
		}
	}
	return out
}

// TrackableOrder é um pedido com etiqueta do Melhor Envio a acompanhar.
type TrackableOrder struct {
	OrderID   int
	SellerID  int
	MEOrderID string
	Status    string
}

// ListTrackableOrders lista os pedidos com etiqueta ainda não entregues.
func ListTrackableOrders() []TrackableOrder {
	rows, err := database.DB.Query(`SELECT id, seller_id, me_order_id, status FROM orders
		WHERE me_order_id <> '' AND status IN ('pago','pronto','enviado')`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TrackableOrder
	for rows.Next() {
		var t TrackableOrder
		if rows.Scan(&t.OrderID, &t.SellerID, &t.MEOrderID, &t.Status) == nil {
			out = append(out, t)
		}
	}
	return out
}

// SellersWithShipments lista vendedores com pedidos pagos nos últimos dias.
func SellersWithShipments(days int) []int {
	rows, err := database.DB.Query(`SELECT DISTINCT seller_id FROM orders WHERE ship_deadline <> '' AND paid_at >= ?`,
		dbTime(time.Now().AddDate(0, 0, -days)))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
