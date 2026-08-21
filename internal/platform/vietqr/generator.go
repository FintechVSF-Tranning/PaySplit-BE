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
	consumer := tlv("00", bankBIN) + tlv("01", accountNumber)
	merchant := tlv("00", napasAID) + tlv("01", consumer) + tlv("02", "QRIBFTTA")
	prefix := tlv("00", "01") + tlv("01", "12") + tlv("38", merchant) + tlv("53", "704") + tlv("54", strconv.FormatInt(amount, 10)) + tlv("58", "VN") + tlv("62", tlv("08", reference)) + "6304"
	payload := prefix + fmt.Sprintf("%04X", crc16([]byte(prefix)))
	path := fmt.Sprintf("%s/%s-%s-%s.png", g.baseURL, url.PathEscape(bankBIN), url.PathEscape(accountNumber), url.PathEscape(g.template))
	query := url.Values{"amount": {strconv.FormatInt(amount, 10)}, "addInfo": {reference}, "accountName": {accountHolder}}.Encode()
	return payload, path + "?" + strings.ReplaceAll(query, "+", "%20"), nil
}

func tlv(tag, value string) string {
	return tag + fmt.Sprintf("%02d", len([]byte(value))) + value
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
