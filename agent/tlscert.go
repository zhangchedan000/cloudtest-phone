package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// startTLS 在 addr 上另起一个 HTTPS 服务（自签证书），和 HTTP 口共用同一套 Handler。
// 为什么要 HTTPS：浏览器的视频解码器 WebCodecs（画面实时视频靠它）只在「安全上下文」才可用——
// HTTPS 或 localhost 算，纯 http 的局域网 IP（http://192.168.x.x）不算，于是画面只能退回每秒一张截图、很卡（踩坑 #170）。
// 证书是自签的：浏览器第一次会报「不安全」，点继续即可（已经是安全上下文、视频能播）；或把 tls/cert.pem 装进「受信任的根」就不再报警。
func startTLS(addr, dataDir string, h http.Handler) {
	cert, err := selfSignedCert(dataDir)
	if err != nil {
		log.Printf("HTTPS 证书没弄好，不开 HTTPS（画面在 http 下只能用截图）：%v", err)
		return
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}
	go func() {
		log.Printf("HTTPS 监听 %s（自签证书，给浏览器视频解码用；证书在 %s/tls/cert.pem）", addr, dataDir)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTPS 监听出错：%v", err)
		}
	}()
}

// selfSignedCert 加载或生成自签证书（缓存在 dataDir/tls/）。证书覆盖本机所有非回环 IPv4 + 127.0.0.1 + localhost，
// 设成 CA（装进「受信任的根」后浏览器就不报警），有效期 10 年。本机新增 / 变更 IP 后删掉 tls/ 重启即可重生成。
func selfSignedCert(dataDir string) (tls.Certificate, error) {
	cd := filepath.Join(dataDir, "tls")
	certPath, keyPath := filepath.Join(cd, "cert.pem"), filepath.Join(cd, "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(c.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil && time.Now().Before(leaf.NotAfter.Add(-24*time.Hour)) && coversLocalIPs(leaf) {
			return c, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "prism-phone 云测机"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           localIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
	// 缓存（best-effort，写不进就每次重生成）
	if err := os.MkdirAll(cd, 0o700); err == nil {
		_ = os.WriteFile(certPath, certPem, 0o600)
		_ = os.WriteFile(keyPath, keyPem, 0o600)
	}
	return tls.X509KeyPair(certPem, keyPem)
}

// localIPs 本机所有非回环 IPv4 + 回环地址（证书 SAN 用）。
func localIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			if v4 := ipn.IP.To4(); v4 != nil {
				ips = append(ips, v4)
			}
		}
	}
	return ips
}

// coversLocalIPs 缓存的证书是否还覆盖本机当前所有 IPv4（换了网络 / IP 就要重生成）。
func coversLocalIPs(leaf *x509.Certificate) bool {
	for _, ip := range localIPs() {
		if ip.IsLoopback() {
			continue
		}
		found := false
		for _, cip := range leaf.IPAddresses {
			if cip.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
