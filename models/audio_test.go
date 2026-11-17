package models

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vendas/loja/database"
)

// mp3Sintetico gera quadros MPEG-1 Layer III de 128 kbps / 44,1 kHz (417
// bytes cada, 1152 amostras): n quadros duram n × 1152 / 44100 segundos.
func mp3Sintetico(n int, comID3 bool) []byte {
	var b bytes.Buffer
	if comID3 {
		b.Write([]byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 20})
		b.Write(make([]byte, 20))
	}
	frame := make([]byte, 417)
	copy(frame, []byte{0xFF, 0xFB, 0x90, 0x64})
	for i := 0; i < n; i++ {
		b.Write(frame)
	}
	return b.Bytes()
}

func m4aSintetico(segundos uint32) []byte {
	box := func(kind string, body []byte) []byte {
		out := make([]byte, 8, 8+len(body))
		binary.BigEndian.PutUint32(out, uint32(8+len(body)))
		copy(out[4:], kind)
		return append(out, body...)
	}
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)          // timescale
	binary.BigEndian.PutUint32(mvhd[16:], segundos*1000) // duração
	ftyp := box("ftyp", []byte("M4B \x00\x00\x02\x00isomM4B "))
	return append(ftyp, box("moov", box("mvhd", mvhd))...)
}

func TestReconheceAudioEDuracao(t *testing.T) {
	dir := t.TempDir()
	mp3 := mp3Sintetico(3828, true) // ≈ 100 s
	if k, err := DetectAudioType(mp3); err != nil || k != AudioMP3 {
		t.Fatalf("MP3: %q %v", k, err)
	}
	m4b := m4aSintetico(4530)
	if k, err := DetectAudioType(m4b); err != nil || k != AudioM4A {
		t.Fatalf("M4B: %q %v", k, err)
	}
	for _, ruim := range [][]byte{[]byte("%PDF-1.7"), []byte("texto qualquer"), {}} {
		if _, err := DetectAudioType(ruim); !errors.Is(err, ErrInvalidAudio) {
			t.Errorf("aceitou %q como áudio", ruim)
		}
	}
	pm, pa := filepath.Join(dir, "a.mp3"), filepath.Join(dir, "a.m4a")
	os.WriteFile(pm, mp3, 0o600)
	os.WriteFile(pa, m4b, 0o600)
	if d := AudioDuration(pm, AudioMP3); d < 99 || d > 101 {
		t.Fatalf("duração MP3 = %d; quer ~100", d)
	}
	if d := AudioDuration(pa, AudioM4A); d != 4530 {
		t.Fatalf("duração M4B = %d; quer 4530", d)
	}
	// Amostra de 30 s: 30 × 128 kbps = 480.000 bytes depois da etiqueta ID3.
	if n, ok := MP3SampleBytes(pm, 30); !ok || n != 30+480000 {
		t.Fatalf("amostra = %d %v", n, ok)
	}
	if DurationLabel(27240) != "7 h 34 min" || DurationLabel(600) != "10 min" {
		t.Fatalf("rótulos: %q %q", DurationLabel(27240), DurationLabel(600))
	}
}

func TestEdicoesEPosseDoAudiobook(t *testing.T) {
	setupDB(t)
	t.Setenv("SETTINGS_ENCRYPTION_KEY", chaveTeste)
	autora := vendedorConectado(t, "autora", cpfTeste1, "111")
	aprovarAutora(t, autora, "Helena Duarte")
	p := ebookNovo(autora, "Helena Duarte", "hash-audio")
	p.Price = 20
	if err := CreateProduct(p); err != nil {
		t.Fatal(err)
	}
	var id int
	database.DB.QueryRow(`SELECT MAX(id) FROM products`).Scan(&id)
	cid64, _ := CreateCustomer("Leitora", "leitora@exemplo.com", "senha-com-12-caracteres")
	cid := int(cid64)

	compra := func(edicao string) (int64, error) {
		o := &Order{CustomerID: cid, CustomerName: "Leitora", CustomerEmail: "leitora@exemplo.com",
			Items: []OrderItem{{ProductID: id, Quantity: 1, Edition: edicao, Price: 0.01}}}
		return CreateOrder(o)
	}
	// Sem faixas, o audiobook não está à venda.
	if _, err := compra(EditionAudio); !errors.Is(err, ErrEditionInvalid) {
		t.Fatalf("vendeu audiobook sem faixas: %v", err)
	}
	if err := AddAudioTrack(AudioTrack{ProductID: id, Title: "Cap. 1", File: "0123456789abcdef0123456789abcdef.mp3",
		Format: AudioMP3, Size: 1000, DurationSec: 600, SHA256: "faixa1"}); err != nil {
		t.Fatal(err)
	}
	// Mesmo arquivo em outro livro: barrado.
	if err := AddAudioTrack(AudioTrack{ProductID: id + 99, File: "fedcba9876543210fedcba9876543210.mp3", Format: AudioMP3, SHA256: "faixa1"}); !errors.Is(err, ErrAudioDuplicate) {
		t.Fatalf("áudio repetido aceito: %v", err)
	}
	if err := SetAudioPrices(id, 0, 45, true); err == nil {
		t.Fatal("combo sem preço de audiobook aceito")
	}
	if err := SetAudioPrices(id, 30, 42, true); err != nil {
		t.Fatal(err)
	}
	prod, _ := GetProduct(id)
	if !prod.HasAudio() || !prod.HasBundle() || prod.AudioSeconds != 600 || prod.AudioTracks != 1 {
		t.Fatalf("produto = %+v", prod)
	}

	// Compra só o audiobook: preço vem do banco, dá acesso ao áudio e não ao e-book.
	oid, err := compra(EditionAudio)
	if err != nil {
		t.Fatal(err)
	}
	o, _ := GetOrder(int(oid))
	if o.Total != 30 || o.Items[0].ProductName != "Livro digital (Audiobook)" {
		t.Fatalf("pedido = %v / %q", o.Total, o.Items[0].ProductName)
	}
	MarkOrderPaid(int(oid), 3000, "pix")
	if !CustomerOwnsAudio(cid, id) || CustomerOwnsProduct(cid, id) {
		t.Fatal("audiobook deu acesso errado")
	}
	if _, err := compra(EditionAudio); !errors.Is(err, ErrAlreadyOwned) {
		t.Fatal("comprou o audiobook duas vezes")
	}
	if _, err := compra(EditionCombo); !errors.Is(err, ErrAlreadyOwned) {
		t.Fatal("combo para quem já tem o áudio")
	}
	// Completa com o e-book.
	oid, err = compra(EditionEbook)
	if err != nil {
		t.Fatal(err)
	}
	MarkOrderPaid(int(oid), 2000, "pix")
	if !CustomerOwnsProduct(cid, id) {
		t.Fatal("e-book comprado depois não liberou")
	}
	lib, _ := ListLibrary(cid)
	if len(lib) != 1 || !lib[0].OwnsEbook || !lib[0].OwnsAudio || lib[0].AudioTracks != 1 {
		t.Fatalf("biblioteca = %+v", lib)
	}
	if _, err := GetAudioTrackForCustomer(cid, id, 1); err != nil {
		t.Fatalf("faixa: %v", err)
	}
	if _, err := GetAudioTrackForCustomer(cid+1, id, 1); err == nil {
		t.Fatal("faixa entregue a quem não comprou")
	}
}
