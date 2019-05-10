// Copyright 2019 Smart-Edge.com, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pki

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"log"
	"math/big"
	rdm "math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
)

// RootCA manages digital certificates.
type RootCA struct {
	Cert *x509.Certificate
	Key  crypto.PrivateKey
}

// InitRootCA creates a RootCA by loading the CA certificate and key from the
// certificates directory. If they do not exist or the certificate was not
// signed with the key, a new certificate and key will generated.
func InitRootCA(certsDir string) (*RootCA, error) {
	var (
		err error

		keyFile string
		key     crypto.PrivateKey

		certFile string
		cert     *x509.Certificate
		certDER  []byte
	)

	if err = os.MkdirAll(certsDir, 0700); err != nil {
		return nil, errors.Wrap(err, "unable to create CA directory")
	}

	keyFile = filepath.Join(certsDir, "key.pem")

	if key, err = LoadKey(keyFile); err != nil {
		if key, err = ecdsa.GenerateKey(
			elliptic.P256(),
			rand.Reader,
		); err != nil {
			return nil, errors.Wrap(err, "unable to generate CA key")
		}

		if err = StoreKey(key, keyFile); err != nil {
			return nil, errors.Wrap(err, "unable to store CA key")
		}

		log.Printf("Generated and stored CA key at: %s", keyFile)
	}

	certFile = filepath.Join(certsDir, "cert.pem")

	if cert, err = LoadCertificate(certFile); err != nil {
		if cert, err = generateRootCA(key); err != nil {
			return nil, errors.Wrap(err, "unable to generate root CA")
		}

		if err = StoreCertificate(certFile, cert); err != nil {
			return nil, errors.Wrap(err, "unable to store CA certificate")
		}

		log.Printf("Generated and stored CA certificate at: %s", certFile)
	}

	if certDER, err = x509.MarshalPKIXPublicKey(
		key.(crypto.Signer).Public(),
	); err != nil {
		return nil, errors.Wrap(err, "unable to marshal public key")
	}

	// Verify the certificate was signed with the private key
	if !bytes.Equal(cert.RawSubjectPublicKeyInfo, certDER) {
		if err = os.Remove(certFile); err != nil {
			return nil, errors.Wrap(err, "unable to remove invalid cert")
		}

		return InitRootCA(certsDir)
	}

	return &RootCA{
		Cert: cert,
		Key:  key,
	}, nil
}

// CAChain returns the root CA certificate wrapped in a slice to satisfy the
// interface. Since the root CA is the issuing CA and there are no intermediate
// CAs, we only need to return the root CA certificate.
func (ca *RootCA) CAChain() ([]*x509.Certificate, error) {
	return []*x509.Certificate{ca.Cert}, nil
}

// SignCSR signs a ASN.1 DER encoded certificate signing request.
func (ca *RootCA) SignCSR(der []byte) (*x509.Certificate, error) {
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, errors.Wrap(err, "unable to parse CSR")
	}

	// Certificate CN is base64-encoded (w/o padding) MD5 hash of the public key
	pub, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return nil, errors.Wrap(err, "unable to marshal CSR public key")
	}

	hash := md5.Sum(pub)

	cn := base64.RawURLEncoding.EncodeToString(hash[:])

	subject := pkix.Name{CommonName: cn}

	source := rdm.NewSource(time.Now().UnixNano())

	serial := big.NewInt(int64(rdm.New(source).Uint64()))

	template := &x509.Certificate{
		Signature:          csr.Signature,
		SignatureAlgorithm: csr.SignatureAlgorithm,
		PublicKey:          csr.PublicKey,
		PublicKeyAlgorithm: csr.PublicKeyAlgorithm,
		SerialNumber:       serial,
		Issuer:             ca.Cert.Subject,
		Subject:            subject,
		NotBefore:          time.Now(),
		NotAfter:           ca.Cert.NotAfter, // Valid until CA expires
	}

	certDER, err := x509.CreateCertificate(
		rand.Reader,
		template,
		ca.Cert,
		template.PublicKey,
		ca.Key,
	)
	if err != nil {
		return nil, errors.Wrap(err, "unable to sign certificate")
	}

	return x509.ParseCertificate(certDER)
}

// generateRootCA creates a root CA from the private key valid for 3 years.
func generateRootCA(key crypto.PrivateKey) (*x509.Certificate, error) {
	var (
		err      error
		k        crypto.Signer
		ok       bool
		source   rdm.Source
		serial   *big.Int
		template *x509.Certificate
		der      []byte
	)

	if k, ok = key.(crypto.Signer); !ok {
		return nil, errors.Wrap(err, "unable to parse key")
	}

	source = rdm.NewSource(time.Now().UnixNano())

	serial = big.NewInt(int64(rdm.New(source).Uint64()))

	template = &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Controller Authority"},
		},
		NotBefore:             time.Now().Add(-15 * time.Second),
		NotAfter:              time.Now().Add(3 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		BasicConstraintsValid: true,
	}

	if der, err = x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		k.Public(),
		key,
	); err != nil {
		return nil, errors.Wrap(err, "unable to create CA certificate")
	}

	return x509.ParseCertificate(der)
}