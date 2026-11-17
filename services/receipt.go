package services

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// Comprovante de compra em PDF.
//
// Não é nota fiscal: NF-e/NFS-e só pode ser emitida por empresa (CNPJ), com
// certificado digital e autorização da Sefaz ou da prefeitura. O documento
// diz isso de forma explícita para ninguém confundir os dois.

type ReceiptItem struct {
	Title     string
	Quantity  int
	UnitPrice string // já formatado (R$ 1.234,56)
	Subtotal  string
}

type Receipt struct {
	StoreName     string
	OrderID       int
	CreatedAt     string
	PaidAt        string
	PaymentMethod string
	CustomerName  string
	CustomerEmail string
	Address       string
	SellerName    string
	SellerKind    string // "Loja", "Pessoa física" ou vazio (o próprio site)
	Items         []ReceiptItem
	Total         string
}

// Cores da marca, em RGB.
var (
	corMusgo    = [3]int{31, 92, 60}
	corCaramelo = [3]int{179, 127, 58}
	corCafe     = [3]int{48, 38, 29}
	corCafeSuav = [3]int{117, 99, 79}
	corPapel    = [3]int{247, 239, 226}
	corLinha    = [3]int{226, 207, 174}
)

// ReceiptPDF monta o comprovante e devolve os bytes do PDF.
func ReceiptPDF(r Receipt) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 22)
	pdf.SetTitle(fmt.Sprintf("Comprovante do pedido %d", r.OrderID), true)
	pdf.SetAuthor(r.StoreName, true)
	// As fontes padrão do PDF usam cp1252, que cobre acentos e cedilha.
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	pdf.SetFooterFunc(func() {
		pdf.SetY(-14)
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
		pdf.CellFormat(0, 5, tr(fmt.Sprintf("Emitido em %s · %s", time.Now().Format("02/01/2006 15:04"), r.StoreName)), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 5, tr(fmt.Sprintf("Página %d", pdf.PageNo())), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	// ── Cabeçalho ──
	pdf.SetFillColor(corMusgo[0], corMusgo[1], corMusgo[2])
	pdf.Rect(0, 0, 210, 38, "F")
	pdf.SetXY(18, 11)
	pdf.SetTextColor(corPapel[0], corPapel[1], corPapel[2])
	pdf.SetFont("Times", "I", 26)
	pdf.CellFormat(0, 10, tr(r.StoreName), "", 1, "L", false, 0, "")
	pdf.SetX(18)
	pdf.SetFont("Helvetica", "B", 8.5)
	pdf.SetTextColor(corCaramelo[0]+40, corCaramelo[1]+50, corCaramelo[2]+60)
	pdf.CellFormat(0, 6, tr("COMPROVANTE DE COMPRA"), "", 1, "L", false, 0, "")
	pdf.SetDrawColor(corCaramelo[0], corCaramelo[1], corCaramelo[2])
	pdf.SetLineWidth(0.6)
	pdf.Line(0, 38, 210, 38)

	// ── Número e aviso ──
	pdf.SetY(48)
	pdf.SetTextColor(corCafe[0], corCafe[1], corCafe[2])
	pdf.SetFont("Times", "", 22)
	pdf.CellFormat(100, 10, tr(fmt.Sprintf("Pedido nº %d", r.OrderID)), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetTextColor(corMusgo[0], corMusgo[1], corMusgo[2])
	pdf.CellFormat(0, 10, tr("PAGO"), "", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
	pdf.MultiCell(0, 4.5, tr("Este comprovante registra a compra e o pagamento. Não é documento fiscal e não substitui nota fiscal."), "", "L", false)
	pdf.Ln(5)

	// ── Dados em duas colunas ──
	campo := func(rotulo, valor string, largura float64, quebra bool) {
		x, y := pdf.GetX(), pdf.GetY()
		pdf.SetFont("Helvetica", "B", 7)
		pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
		pdf.CellFormat(largura, 4, tr(rotulo), "", 2, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(corCafe[0], corCafe[1], corCafe[2])
		pdf.MultiCell(largura, 5, tr(valor), "", "L", false)
		if !quebra {
			pdf.SetXY(x+largura, y)
		} else {
			pdf.Ln(2)
		}
	}
	vendedor := r.SellerName
	if r.SellerKind != "" {
		vendedor += " (" + r.SellerKind + ")"
	}
	campo("DATA DO PEDIDO", r.CreatedAt, 87, false)
	campo("PAGAMENTO CONFIRMADO EM", valorOuTraco(r.PaidAt), 87, true)
	campo("FORMA DE PAGAMENTO", r.PaymentMethod, 87, false)
	campo("VENDIDO POR", vendedor, 87, true)
	campo("COMPRADOR", r.CustomerName, 87, false)
	campo("E-MAIL", r.CustomerEmail, 87, true)
	campo("ENDEREÇO DE ENTREGA", r.Address, 174, true)
	pdf.Ln(4)

	// ── Itens ──
	cols := []float64{96, 16, 31, 31}
	pdf.SetFillColor(corPapel[0], corPapel[1], corPapel[2])
	pdf.SetDrawColor(corLinha[0], corLinha[1], corLinha[2])
	pdf.SetLineWidth(0.2)
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
	for i, titulo := range []string{"TÍTULO", "QTD.", "UNITÁRIO", "SUBTOTAL"} {
		alinhamento := "R"
		if i == 0 {
			alinhamento = "L"
		}
		pdf.CellFormat(cols[i], 8, tr(titulo), "B", 0, alinhamento, true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(corCafe[0], corCafe[1], corCafe[2])
	for _, item := range r.Items {
		pdf.CellFormat(cols[0], 8, tr(truncar(item.Title, 58)), "B", 0, "L", false, 0, "")
		pdf.CellFormat(cols[1], 8, fmt.Sprintf("%d", item.Quantity), "B", 0, "R", false, 0, "")
		pdf.CellFormat(cols[2], 8, tr(item.UnitPrice), "B", 0, "R", false, 0, "")
		pdf.CellFormat(cols[3], 8, tr(item.Subtotal), "B", 1, "R", false, 0, "")
	}

	// ── Total ──
	pdf.Ln(4)
	pdf.SetDrawColor(corCaramelo[0], corCaramelo[1], corCaramelo[2])
	pdf.SetLineWidth(0.5)
	y := pdf.GetY()
	pdf.Line(110, y, 192, y)
	pdf.Ln(2)
	pdf.SetX(110)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
	pdf.CellFormat(40, 10, "Total", "", 0, "L", false, 0, "")
	pdf.SetFont("Times", "", 18)
	pdf.SetTextColor(corCaramelo[0]-40, corCaramelo[1]-50, corCaramelo[2]-20)
	pdf.CellFormat(42, 10, tr(r.Total), "", 1, "R", false, 0, "")

	pdf.Ln(10)
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.SetTextColor(corCafeSuav[0], corCafeSuav[1], corCafeSuav[2])
	pdf.MultiCell(0, 4.5, tr("Pagamento processado pelo Mercado Pago. Guarde este comprovante: ele identifica o seu pedido em qualquer contato com o vendedor."), "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func valorOuTraco(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func truncar(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
