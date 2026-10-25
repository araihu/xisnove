# X-9 site

Static public product site for `x9.araihu.com`, generated with Go, templ, and Goshtoso assets.
Identity uses canonical adaptive X-9 v11 logo and icon from
`araihu/assets` release `v0.2.5`. The root `araihu-assets.json` pins the release,
checksums, and repository-owned file mappings. The Assets consumer workflow
verifies release provenance and catalog selections before opening an update PR.

```sh
make check
```

Deploys as a Cloudflare Worker with static assets on the exact custom domain
`x9.araihu.com`:

```sh
make deploy-dry-run
make deploy
```
