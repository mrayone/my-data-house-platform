module github.com/mrayone/my-data-house-platform/tools

go 1.23

require mvdan.cc/gofumpt v0.7.0

// golang.org/x/* não é resolvível pela rede deste ambiente (domínio de vanity
// import bloqueado); replace direto para os espelhos em github.com/golang/*.
// Ver docs/TECH-DEBT.md.
replace (
	golang.org/x/mod => github.com/golang/mod v0.39.0
	golang.org/x/sync => github.com/golang/sync v0.23.0
	golang.org/x/tools => github.com/golang/tools v0.39.0
)

replace mvdan.cc/gofumpt => github.com/mvdan/gofumpt v0.7.0
