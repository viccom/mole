// 本目录是纯手机端前端资产（无第一方 Go 代码）；独立 go.mod 将其从
// server 模块图切出——否则 npm install 拉进的 node_modules/*/golang 绑定
// （如 flatted）会被 `go list ./...` 收进包集合，使本机 vet/test 的包集
// 与干净 clone / CI 不一致（2026-10-02 B-5）。
module moleAgent_Serv/mobile

go 1.25.8
