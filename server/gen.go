package server

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"

	"encoding/base32"
	"encoding/base64"
	"encoding/hex"

	"crypto/rand"
	"crypto/sha512"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/sha3"
)

func GenKey(prefix string, count int) {
	found := make(chan bool)

	go func(found chan bool) {
		for range found {
			count--
			if count == 0 {
				os.Exit(0)
			}
		}

	}(found)

	if prefix == "*" {
		prefix = ""
	}
	r := regexp.MustCompile(".*")
	if len(prefix) > 0 {
		r = regexp.MustCompile(fmt.Sprintf("^(?i)%s", prefix))
	}
	for index := 0; index < runtime.NumCPU(); index++ {
		go search(index, r, found)
	}

	<-context.Background().Done()
}

// Hidden service version
const version = byte(0x03)

// Salt used to create checkdigits
const salt = ".onion checksum"

func search(id int, r *regexp.Regexp, found chan bool) {
	count := 0
	for {
		count++
		pub, pri, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}

		if r.MatchString(base32.StdEncoding.EncodeToString(pub[:])) {
			priv, err := crypto.UnmarshalSecp256k1PrivateKey(pri.Seed())
			if err != nil {
				log.Fatal(err)
			}

			peerid, err := peer.IDFromPrivateKey(priv)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println()
			fmt.Println("Address:", getServiceID(pub)+".onion")
			fmt.Println("Peer ID:", peerid.String())
			fmt.Println("Private Key:", expandKey(pri))
			fmt.Println("Seed: ", hex.EncodeToString(pri.Seed()))
			found <- true
		} else {
			if id == 0 && count%10000 == 0 {
				log.Println("Count: ", count)
			}
		}
	}
}

// Expand ed25519.PrivateKey to (a || RH) form, return base64
func expandKey(pri ed25519.PrivateKey) string {
	h := sha512.Sum512(pri[:32])
	// Set bits so that h[:32] is private scalar "a"
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	// Since h[32:] is RH, h is now (a || RH)
	return base64.StdEncoding.EncodeToString(h[:])
}

func getCheckdigits(pub ed25519.PublicKey) []byte {
	// Calculate checksum sha3(".onion checksum" || publicKey || version)
	checkstr := []byte(salt)
	checkstr = append(checkstr, pub...)
	checkstr = append(checkstr, version)
	checksum := sha3.Sum256(checkstr)
	return checksum[:2]
}

func getServiceID(pub ed25519.PublicKey) string {
	// Construct onion address base32(publicKey || checkdigits || version)
	checkdigits := getCheckdigits(pub)
	combined := pub[:]
	combined = append(combined, checkdigits...)
	combined = append(combined, version)
	serviceID := base32.StdEncoding.EncodeToString(combined)
	return strings.ToLower(serviceID)
}
