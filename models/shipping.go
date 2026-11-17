package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vendas/loja/database"
)

// Frete e envio.
//
// Cada vendedor (e o próprio site, seller_id 0) cadastra de onde despacha e
// conecta a conta Melhor Envio. O frete é cotado no checkout e cobrado do
// comprador; o valor vai para quem envia, que paga a etiqueta com o saldo da
// própria conta Melhor Envio.

// Pacote padrão de um livro embalado, usado quando o vendedor não informa as
// medidas (e em livros cadastrados antes do campo existir).
const (
	DefaultBookWeightG = 400
	DefaultBookHeight  = 4
	DefaultBookWidth   = 16
	DefaultBookLength  = 23
)

var (
	ErrInvalidAddress = errors.New("endereço de entrega incompleto")
	ErrNoShipOrigin   = errors.New("endereço de envio do vendedor não cadastrado")
)

// ── Medidas do livro ─────────────────────────────────────────────────────────

// PackageWeightG devolve o peso para o frete, com o padrão de livro se vazio.
func (p Product) PackageWeightG() int { return orDefault(p.WeightG, DefaultBookWeightG) }
func (p Product) PackageHeight() int  { return orDefault(p.HeightCm, DefaultBookHeight) }
func (p Product) PackageWidth() int   { return orDefault(p.WidthCm, DefaultBookWidth) }
func (p Product) PackageLength() int  { return orDefault(p.LengthCm, DefaultBookLength) }

func orDefault(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

// validatePackage confere peso e medidas do livro físico. Zero vira o pacote
// padrão; valores fora dos limites dos Correios são recusados.
func validatePackage(p *Product) error {
	p.WeightG, p.HeightCm = p.PackageWeightG(), p.PackageHeight()
	p.WidthCm, p.LengthCm = p.PackageWidth(), p.PackageLength()
	if p.WeightG > 30000 {
		return fmt.Errorf("%w: o peso passa de 30 kg", ErrInvalidProduct)
	}
	if p.HeightCm > 100 || p.WidthCm > 100 || p.LengthCm > 100 {
		return fmt.Errorf("%w: medidas acima de 100 cm", ErrInvalidProduct)
	}
	return nil
}

// ── CEP, UF e endereço ───────────────────────────────────────────────────────

// NormalizeCEP devolve só os 8 dígitos do CEP, ou vazio se não for um CEP.
func NormalizeCEP(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() != 8 {
		return ""
	}
	return b.String()
}

// FormatCEP devolve "01310-100".
func FormatCEP(cep string) string {
	if len(cep) != 8 {
		return cep
	}
	return cep[:5] + "-" + cep[5:]
}

var ufs = map[string]bool{
	"AC": true, "AL": true, "AP": true, "AM": true, "BA": true, "CE": true, "DF": true, "ES": true, "GO": true,
	"MA": true, "MT": true, "MS": true, "MG": true, "PA": true, "PB": true, "PR": true, "PE": true, "PI": true,
	"RJ": true, "RN": true, "RS": true, "RO": true, "RR": true, "SC": true, "SP": true, "SE": true, "TO": true,
}

// Address é um endereço estruturado, como as etiquetas pedem.
type Address struct {
	PostalCode string
	Street     string
	Number     string
	Complement string
	District   string
	City       string
	State      string
}

// Normalize limpa os campos e confere que o endereço serve para uma etiqueta.
func (a *Address) Normalize() error {
	a.PostalCode = NormalizeCEP(a.PostalCode)
	for _, f := range []*string{&a.Street, &a.Number, &a.Complement, &a.District, &a.City} {
		*f = strings.Join(strings.Fields(*f), " ")
		if len([]rune(*f)) > 120 {
			*f = string([]rune(*f)[:120])
		}
	}
	a.State = strings.ToUpper(strings.TrimSpace(a.State))
	if a.PostalCode == "" || a.Street == "" || a.Number == "" || a.District == "" || a.City == "" || !ufs[a.State] {
		return ErrInvalidAddress
	}
	return nil
}

// String devolve o endereço numa linha, para a página do pedido e o
// comprovante.
func (a Address) String() string {
	linha := a.Street + ", " + a.Number
	if a.Complement != "" {
		linha += " - " + a.Complement
	}
	return fmt.Sprintf("%s - %s, %s/%s - CEP %s", linha, a.District, a.City, a.State, FormatCEP(a.PostalCode))
}

// ── Origem do envio e conta Melhor Envio ─────────────────────────────────────

// ShippingOrigin é de onde um vendedor despacha (SellerID 0 = o site).
type ShippingOrigin struct {
	SellerID int
	Address
	Phone       string
	MEConnected bool
}

// Complete diz se o endereço serve para gerar etiqueta.
func (o ShippingOrigin) Complete() bool {
	a := o.Address
	return a.Normalize() == nil
}

// GetShippingOrigin devolve a origem do vendedor (vazia se nunca cadastrada).
func GetShippingOrigin(sellerID int) ShippingOrigin {
	o := ShippingOrigin{SellerID: sellerID}
	var token string
	database.DB.QueryRow(`SELECT postal_code, street, number, complement, district, city, state, phone, me_access_token
		FROM shipping_origins WHERE seller_id=?`, sellerID).
		Scan(&o.PostalCode, &o.Street, &o.Number, &o.Complement, &o.District, &o.City, &o.State, &o.Phone, &token)
	o.MEConnected = token != ""
	return o
}

// SaveShippingOrigin grava o endereço de despacho.
func SaveShippingOrigin(sellerID int, a Address, phone string) error {
	if err := a.Normalize(); err != nil {
		return err
	}
	phone = strings.TrimSpace(phone)
	if len(phone) > 30 {
		phone = phone[:30]
	}
	_, err := database.DB.Exec(`INSERT INTO shipping_origins (seller_id, postal_code, street, number, complement, district, city, state, phone)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(seller_id) DO UPDATE SET postal_code=excluded.postal_code, street=excluded.street, number=excluded.number,
			complement=excluded.complement, district=excluded.district, city=excluded.city, state=excluded.state,
			phone=excluded.phone, updated_at=CURRENT_TIMESTAMP`,
		sellerID, a.PostalCode, a.Street, a.Number, a.Complement, a.District, a.City, a.State, phone)
	return err
}

// MEAccount são os tokens da conta Melhor Envio de um vendedor.
type MEAccount struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// SaveMETokens grava os tokens criptografados (como os do Mercado Pago).
func SaveMETokens(sellerID int, access, refresh string, expiresAt time.Time) error {
	if access == "" {
		return fmt.Errorf("token vazio")
	}
	encAccess, err := encryptSetting(access)
	if err != nil {
		return err
	}
	encRefresh := ""
	if refresh != "" {
		if encRefresh, err = encryptSetting(refresh); err != nil {
			return err
		}
	}
	_, err = database.DB.Exec(`INSERT INTO shipping_origins (seller_id, me_access_token, me_refresh_token, me_expires_at)
		VALUES (?,?,?,?)
		ON CONFLICT(seller_id) DO UPDATE SET me_access_token=excluded.me_access_token, me_refresh_token=excluded.me_refresh_token,
			me_expires_at=excluded.me_expires_at, updated_at=CURRENT_TIMESTAMP`,
		sellerID, encAccess, encRefresh, expiresAt.UTC().Format(time.RFC3339))
	return err
}

// GetMEAccount devolve os tokens decifrados, ou sql.ErrNoRows se a conta não
// estiver conectada.
func GetMEAccount(sellerID int) (*MEAccount, error) {
	var access, refresh, expires string
	err := database.DB.QueryRow(`SELECT me_access_token, me_refresh_token, me_expires_at FROM shipping_origins WHERE seller_id=?`, sellerID).
		Scan(&access, &refresh, &expires)
	if err != nil {
		return nil, err
	}
	if access == "" {
		return nil, sql.ErrNoRows
	}
	acc := &MEAccount{}
	if acc.AccessToken, err = decryptSetting(access); err != nil {
		return nil, err
	}
	if refresh != "" {
		if acc.RefreshToken, err = decryptSetting(refresh); err != nil {
			return nil, err
		}
	}
	acc.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
	return acc, nil
}

// DisconnectME apaga os tokens; o endereço de despacho continua.
func DisconnectME(sellerID int) error {
	_, err := database.DB.Exec(`UPDATE shipping_origins SET me_access_token='', me_refresh_token='', me_expires_at='' WHERE seller_id=?`, sellerID)
	return err
}

// ── Frete do pedido ──────────────────────────────────────────────────────────

// Shipping é o frete escolhido no checkout.
type Shipping struct {
	Cents     int64
	ServiceID int    // 0 = frete combinado com o vendedor (sem cotação)
	Service   string // "Correios SEDEX"
	Days      int
}

// Reais devolve o frete em reais.
func (s Shipping) Reais() float64 { return float64(s.Cents) / 100 }

// ShippingLabel é o texto do frete no pedido.
func (o Order) ShippingLabel() string {
	switch {
	case o.Shipping.Service != "":
		return o.Shipping.Service
	case o.HasPhysical():
		return "Frete combinado com o vendedor"
	default:
		return ""
	}
}

// FormattedShipping devolve o valor do frete formatado.
func (o Order) FormattedShipping() string { return FormatBRL(o.Shipping.Reais()) }

// ItemsTotal é o valor dos livros, sem o frete: é sobre ele que incide a
// comissão do site.
func (o Order) ItemsTotal() float64 { return o.Total - o.Shipping.Reais() }

// CanGenerateLabel diz se a etiqueta pode ser gerada: pedido pago, com livro
// para enviar, frete do Melhor Envio e sem etiqueta ainda.
func (o Order) CanGenerateLabel() bool {
	return o.IsPaid() && o.HasPhysical() && o.Shipping.ServiceID > 0 && o.LabelURL == "" && o.Status != "entregue"
}

// TrackingURL leva ao rastreio do pacote.
func (o Order) TrackingURL() string {
	if o.TrackingCode == "" {
		return ""
	}
	return "https://www.melhorrastreio.com.br/rastreio/" + o.TrackingCode
}

// SetOrderLabel grava o que a geração da etiqueta devolveu.
func SetOrderLabel(orderID int, meOrderID, labelURL, tracking string) error {
	_, err := database.DB.Exec(`UPDATE orders SET me_order_id=?, label_url=?, tracking_code=CASE WHEN ?<>'' THEN ? ELSE tracking_code END WHERE id=?`,
		meOrderID, labelURL, tracking, tracking, orderID)
	return err
}

// SetOrderTracking grava o código de rastreio informado à mão (ou atualizado).
func SetOrderTracking(orderID int, code string) error {
	code = strings.ToUpper(strings.Join(strings.Fields(code), ""))
	if len(code) > 40 {
		return fmt.Errorf("código de rastreio muito longo")
	}
	_, err := database.DB.Exec(`UPDATE orders SET tracking_code=? WHERE id=?`, code, orderID)
	return err
}
