package vietqr

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const napasAID = "A000000727"

type Generator struct {
	baseURL  string
	template string
}

func New(baseURL, template string) *Generator {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://img.vietqr.io/image"
	}
	if strings.TrimSpace(template) == "" {
		template = "compact"
	}
	return &Generator{baseURL: baseURL, template: strings.TrimSpace(template)}
}

func (g *Generator) Build(bankBIN, accountNumber, accountHolder, reference string, amount int64) (string, string, error) {
	bankBIN = strings.TrimSpace(bankBIN)
	accountNumber = strings.TrimSpace(accountNumber)
	accountHolder = strings.TrimSpace(accountHolder)
	reference = strings.TrimSpace(reference)
	if bankBIN == "" || accountNumber == "" || accountHolder == "" || reference == "" || amount <= 0 {
		return "", "", errors.New("invalid VietQR input")
	}
	consumer, err := tlvFields("00", bankBIN, "01", accountNumber)
	if err != nil {
		return "", "", err
	}
	merchant, err := tlvFields("00", napasAID, "01", consumer, "02", "QRIBFTTA")
	if err != nil {
		return "", "", err
	}
	additional, err := tlvFields("08", reference)
	if err != nil {
		return "", "", err
	}
	prefix, err := tlvFields("00", "01", "01", "12", "38", merchant, "53", "704", "54", strconv.FormatInt(amount, 10), "58", "VN", "62", additional)
	if err != nil {
		return "", "", err
	}
	prefix += "6304"
	payload := prefix + fmt.Sprintf("%04X", crc16([]byte(prefix)))
	path := fmt.Sprintf("%s/%s-%s-%s.png", g.baseURL, url.PathEscape(bankBIN), url.PathEscape(accountNumber), url.PathEscape(g.template))
	query := url.Values{"amount": {strconv.FormatInt(amount, 10)}, "addInfo": {reference}, "accountName": {accountHolder}}.Encode()
	return payload, path + "?" + strings.ReplaceAll(query, "+", "%20"), nil
}

func tlvFields(fields ...string) (string, error) {
	if len(fields)%2 != 0 {
		return "", errors.New("invalid TLV field list")
	}
	var value strings.Builder
	for i := 0; i < len(fields); i += 2 {
		tag, field := fields[i], fields[i+1]
		length := len([]byte(field))
		if len(tag) != 2 || length > 99 {
			return "", fmt.Errorf("VietQR TLV field %q exceeds its two-digit length", tag)
		}
		value.WriteString(tag)
		value.WriteString(fmt.Sprintf("%02d", length))
		value.WriteString(field)
	}
	return value.String(), nil
}

func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
