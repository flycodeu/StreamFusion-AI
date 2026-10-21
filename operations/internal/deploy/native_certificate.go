package deploy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"time"
)

// ZLM loads a certificate even with HTTPS disabled; WebRTC uses its fingerprint
// for DTLS. Generate an instance key rather than shipping a shared sample key.
func createNativeCertificate(filename, name string, gid int) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})...)
	return serviceFile(filename, data, gid, true)
}

func validateNativeCertificate(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil || len(data) > 16<<10 {
		return errors.New("NATIVE_ZLM_CERTIFICATE_MISSING")
	}
	pair, err := tls.X509KeyPair(data, data)
	if err != nil || len(pair.Certificate) == 0 {
		return errors.New("NATIVE_ZLM_CERTIFICATE_INVALID")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	now := time.Now()
	if err != nil || now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return errors.New("NATIVE_ZLM_CERTIFICATE_EXPIRED")
	}
	return nil
}
