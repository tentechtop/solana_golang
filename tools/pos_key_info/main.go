package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

type keyInfo struct {
	Seed             string `json:"seed"`
	AccountPublicKey string `json:"account_public_key"`
	PeerID           string `json:"peer_id"`
	BLSPublicKey     string `json:"bls_public_key,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		exitError("usage: go run ./tools/pos_key_info <seed> [seed...]")
	}
	infos := make([]keyInfo, 0, len(os.Args)-1)
	for _, seed := range os.Args[1:] {
		info, err := deriveKeyInfo(seed)
		if err != nil {
			exitError("derive %q: %v", seed, err)
		}
		infos = append(infos, info)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(infos); err != nil {
		exitError("encode key info: %v", err)
	}
}

func deriveKeyInfo(seedText string) (keyInfo, error) {
	seedText = strings.TrimSpace(seedText)
	accountKeyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return keyInfo{}, err
	}
	privateKey := utils.SHA256([]byte(seedText))
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return keyInfo{}, err
	}
	blsKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return keyInfo{}, err
	}
	return keyInfo{
		Seed:             seedText,
		AccountPublicKey: accountKeyPair.PublicKey.String(),
		PeerID:           utils.Base58Encode(publicKey),
		BLSPublicKey:     utils.Base58Encode(blsKeyPair.PublicKey[:]),
	}, nil
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
