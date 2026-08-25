// Package shielded 提供 V2 屏蔽钱包所需的密钥、票据加密和承诺树基础能力。
//
// 本包不复用透明账户的 Ed25519 身份。SpendingKey 在 Groth16 电路内完成花费授权，
// 收款使用独立派生的 X25519 transmission key，ViewKey 仅用于扫描和计算 nullifier。
package shielded
