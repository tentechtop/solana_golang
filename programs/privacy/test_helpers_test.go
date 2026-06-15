package privacy

func newTestPublicKey(seed byte) PublicKey {
	var publicKey PublicKey
	for index := range publicKey {
		publicKey[index] = seed
	}
	return publicKey
}

func newTestHash(seed byte) Hash {
	var hash Hash
	for index := range hash {
		hash[index] = seed
	}
	return hash
}
