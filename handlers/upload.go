package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/vendas/loja/models"
)

// maxImageBytes limita o arquivo aceito. O limite do ParseMultipartForm sozinho
// não basta: ele controla o buffer em memória, não o tamanho gravado em disco.
const maxImageBytes = 4 << 20 // 4 MB

const placeholderImage = "/static/img/placeholder.svg"

var ErrNoImage = errors.New("nenhuma imagem enviada")
var ErrInvalidImage = errors.New("envie uma imagem JPEG, PNG, WebP ou GIF de até 4 MB")

// contentTypeExtension mapeia o tipo detectado a partir do conteúdo real do
// arquivo. Confiar na extensão do nome enviado permitiria subir um arquivo
// arbitrário apenas renomeando-o para .png.
var contentTypeExtension = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// saveImageUpload grava a imagem do formulário e devolve o caminho público.
// Retorna ErrNoImage quando o campo veio vazio, para que quem chama decida
// entre manter a imagem atual ou usar o placeholder.
func saveImageUpload(r *http.Request, field string) (string, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		return "", ErrNoImage
	}
	defer file.Close()

	if header.Size > maxImageBytes {
		return "", ErrInvalidImage
	}

	ext, err := detectImageExtension(file)
	if err != nil {
		return "", err
	}

	dir := filepath.Join("static", "img", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// Nome aleatório: o nome original é controlado por quem envia e não deve
	// influenciar o caminho gravado em disco.
	name, err := randomFileName(ext)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(dir, name)

	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// LimitReader corta em maxImageBytes+1 para detectar quem declarou um
	// Content-Length menor que o corpo real.
	written, err := io.Copy(out, io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		os.Remove(destination)
		return "", err
	}
	if written > maxImageBytes {
		os.Remove(destination)
		return "", ErrInvalidImage
	}

	return "/static/img/uploads/" + name, nil
}

// detectImageExtension lê os primeiros bytes para descobrir o tipo real e
// devolve o cursor ao início para a gravação.
func detectImageExtension(file multipart.File) (string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	contentType := http.DetectContentType(head[:n])
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	ext, ok := contentTypeExtension[contentType]
	if !ok {
		return "", ErrInvalidImage
	}
	return ext, nil
}

func randomFileName(ext string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", hex.EncodeToString(raw[:]), ext), nil
}

// removeUploadedImage apaga do disco a foto de um produto excluído. Só mexe em
// arquivos da pasta de uploads e ignora a imagem se outro produto ainda a usa.
func removeUploadedImage(imageURL string) {
	const prefixo = "/static/img/uploads/"
	if !strings.HasPrefix(imageURL, prefixo) || models.ImageInUse(imageURL) {
		return
	}
	nome := filepath.Base(imageURL)
	if nome == "." || nome == "/" || strings.Contains(nome, "..") {
		return
	}
	if err := os.Remove(filepath.Join("static", "img", "uploads", nome)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("não foi possível apagar a imagem %s: %v", nome, err)
	}
}

// deleteProduct aplica a exclusão e limpa a foto quando o produto sai de vez.
func deleteProduct(id int) error {
	var ebookFile string
	if p, err := models.GetProduct(id); err == nil {
		ebookFile = p.EbookFile
	}
	removed, imageURL, err := models.DeleteProduct(id)
	if err != nil {
		return err
	}
	// Livro já vendido é arquivado, não apagado: o e-book continua no disco
	// para quem comprou poder baixar.
	if removed {
		removeUploadedImage(imageURL)
		removeEbookFile(ebookFile)
	}
	return nil
}

// resolveProductImage centraliza a regra usada pelos dois painéis: imagem nova
// substitui a anterior; sem imagem nova, mantém a que já existia; e um produto
// novo sem imagem recebe o placeholder.
func resolveProductImage(r *http.Request, currentURL string) (string, error) {
	uploaded, err := saveImageUpload(r, "imagem")
	switch {
	case err == nil:
		return uploaded, nil
	case errors.Is(err, ErrNoImage):
		// Capa encontrada pela busca de ISBN do formulário.
		if capa := strings.TrimSpace(r.FormValue("capa_url")); capa != "" {
			u, err := saveRemoteImage(capa)
			if err == nil {
				return u, nil
			}
			log.Printf("capa pelo ISBN: %v", err)
		}
		if currentURL != "" {
			return currentURL, nil
		}
		return placeholderImage, nil
	default:
		return "", err
	}
}
