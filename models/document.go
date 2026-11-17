package models

import (
	"errors"
	"fmt"
)

// ErrInvalidDocument sinaliza um CPF ou CNPJ que falhou na validação do
// dígito verificador oficial, ou que não tem 11 nem 14 dígitos.
var ErrInvalidDocument = errors.New("CPF ou CNPJ inválido")

// ErrDocumentInUse impede duas contas de vendedor usarem o mesmo documento.
var ErrDocumentInUse = errors.New("este CPF/CNPJ já está cadastrado")

// NormalizeDocument mantém só os dígitos, para que "123.456.789-09" e
// "12345678909" sejam tratados como o mesmo documento na hora de checar
// duplicidade.
func NormalizeDocument(raw string) string {
	digits := make([]byte, 0, len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits = append(digits, byte(r))
		}
	}
	return string(digits)
}

// ValidateDocument normaliza o texto informado e confere o dígito
// verificador oficial de CPF (11 dígitos) ou CNPJ (14 dígitos). Retorna o
// documento já normalizado, pronto para gravar.
func ValidateDocument(raw string) (string, error) {
	digits := NormalizeDocument(raw)

	// "111.111.111-11" e documentos assim passam no cálculo do dígito
	// verificador dos dois algoritmos abaixo, mas nunca foram emitidos — é o
	// golpe mais comum contra esse tipo de validação, por isso a rejeição é
	// explícita e não depende só do checksum.
	if allSameDigit(digits) {
		return "", ErrInvalidDocument
	}

	switch len(digits) {
	case 11:
		if !validCPF(digits) {
			return "", ErrInvalidDocument
		}
	case 14:
		if !validCNPJ(digits) {
			return "", ErrInvalidDocument
		}
	default:
		return "", ErrInvalidDocument
	}
	return digits, nil
}

// DocumentKind diz se o documento é de loja (CNPJ) ou de pessoa física (CPF).
func DocumentKind(digits string) string {
	switch len(digits) {
	case 14:
		return "Loja"
	case 11:
		return "Pessoa física"
	default:
		return ""
	}
}

// FormatDocument aplica a máscara usual de exibição. Recebe o documento já
// normalizado (só dígitos); qualquer outro tamanho volta sem máscara.
func FormatDocument(digits string) string {
	switch len(digits) {
	case 11:
		return fmt.Sprintf("%s.%s.%s-%s", digits[0:3], digits[3:6], digits[6:9], digits[9:11])
	case 14:
		return fmt.Sprintf("%s.%s.%s/%s-%s", digits[0:2], digits[2:5], digits[5:8], digits[8:12], digits[12:14])
	default:
		return digits
	}
}

func allSameDigit(digits string) bool {
	if digits == "" {
		return false
	}
	for i := 1; i < len(digits); i++ {
		if digits[i] != digits[0] {
			return false
		}
	}
	return true
}

// digitCheckSum calcula o resto usado pelos dígitos verificadores de CPF e
// CNPJ: soma ponderada dos dígitos, módulo 11, convertido em dígito.
func digitCheckSum(digits string, length int, weights []int) int {
	sum := 0
	for i := 0; i < length; i++ {
		sum += int(digits[i]-'0') * weights[i]
	}
	remainder := sum % 11
	if remainder < 2 {
		return 0
	}
	return 11 - remainder
}

func validCPF(d string) bool {
	if digitCheckSum(d, 9, []int{10, 9, 8, 7, 6, 5, 4, 3, 2}) != int(d[9]-'0') {
		return false
	}
	return digitCheckSum(d, 10, []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2}) == int(d[10]-'0')
}

func validCNPJ(d string) bool {
	if digitCheckSum(d, 12, []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}) != int(d[12]-'0') {
		return false
	}
	return digitCheckSum(d, 13, []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}) == int(d[13]-'0')
}
