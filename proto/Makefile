# Single codegen entrypoint for the shared proto module.
#
# Before this consolidation, a proto change meant running codegen in up to four
# places (fulfillment-service/dev.py + buf generate, then `buf generate` again in
# osac-operator, osac-csi-driver and osac-metering/metering-service). Now there
# is one generated Go tree, under proto/gen/, that every module imports -- so one
# command regenerates everything.

.PHONY: generate lint clean

# Step 1: regenerate proto/public/ from proto/private/ via protoc-gen-cleanapi.
#         Still driven by fulfillment-service/dev.py, which owns the cleanapi
#         toolchain (protoc plugin install, version pinning).
# Step 2: regenerate the single Go tree under proto/gen/ (buf.gen.yaml).
generate:
	cd ../fulfillment-service && uv run dev.py build protos
	buf generate

lint:
	cd ../fulfillment-service && uv run dev.py lint proto

clean:
	rm -rf .buf bin
