module example.com/xisnove-extension

go 1.26.0

toolchain go1.26.5

require github.com/araihu/xisnove v0.0.0

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/oapi-codegen/runtime v1.6.0 // indirect
)

replace github.com/araihu/xisnove => ../../..
