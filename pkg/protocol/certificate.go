package protocol

import (
	"fmt"
)

type Certificate struct {
	CertificatesLength uint32 // 3个字节
	Certificates       []struct {
		CertificateLength uint32 // 3个字节
		Certificate       []byte
	}
}

// ParseCertificate 从 []byte 数据解析出 Certificate 结构体
func ParseCertificate(data []byte) (*Certificate, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("TLS Handshake Data is incomplete")
	}
	offset := 0
	certificatesLength := uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
	offset += 3
	certificate := &Certificate{CertificatesLength: certificatesLength}
	for offset < len(data) {
		if offset+3 > len(data) {
			return nil, fmt.Errorf("Certificate Entry is invalid")
		}
		certificateLength := uint32(data[offset])<<16 | uint32(data[offset+1])<<8 | uint32(data[offset+2])
		offset += 3
		certificate.Certificates = append(certificate.Certificates, struct {
			CertificateLength uint32
			Certificate       []byte
		}{CertificateLength: certificateLength, Certificate: data[offset : offset+int(certificateLength)]})
		offset += int(certificateLength)
	}
	return certificate, nil
}

func (c *Certificate) GetRaw() []byte {
	certificatesLength := []byte{byte(c.CertificatesLength & 0xff), byte((c.CertificatesLength >> 8) & 0xff), byte((c.CertificatesLength >> 16) & 0xff)}
	certificates := certificatesLength
	for _, certificate := range c.Certificates {
		certificateLength := []byte{byte(c.CertificatesLength & 0xff), byte((c.CertificatesLength >> 8) & 0xff), byte((c.CertificatesLength >> 16) & 0xff)}
		certificates = append(certificates, certificateLength...)
		certificates = append(certificates, certificate.Certificate...)
	}
	return certificates
}