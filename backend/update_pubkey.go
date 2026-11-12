package main

// 在线更新签名公钥（ed25519）。由 go run ./cmd/updsign keygen 生成。
// 更换公钥会让所有已安装版本拒绝新的更新包——除非先发一个同时认新旧两把钥匙的过渡版本。
const updatePubKey = "VWUnmbRIXFO/AKA53No5QyXbjys1z+WaUWFXHkSvMWc="
