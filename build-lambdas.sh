#!/bin/bash
set -e

echo "🔨 Building NextDeploy Auxiliary Lambdas..."

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Build imgopt Lambda
echo -e "${BLUE}Building imgopt Lambda...${NC}"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o assets/lambda/imgopt/bootstrap ./cmd/imgopt/main.go
echo -e "${GREEN}✓ imgopt Lambda built${NC}"

# Build revalidator Lambda
echo -e "${BLUE}Building revalidator Lambda...${NC}"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o assets/lambda/revalidator/bootstrap ./cmd/revalidator/main.go
echo -e "${GREEN}✓ revalidator Lambda built${NC}"

# Verify binaries exist and are not empty
if [ ! -s assets/lambda/imgopt/bootstrap ]; then
    echo "❌ Error: imgopt bootstrap is empty or missing"
    exit 1
fi

if [ ! -s assets/lambda/revalidator/bootstrap ]; then
    echo "❌ Error: revalidator bootstrap is empty or missing"
    exit 1
fi

echo ""
echo -e "${GREEN}✅ All auxiliary Lambdas built successfully!${NC}"
echo ""
echo "Binary sizes:"
ls -lh assets/lambda/imgopt/bootstrap
ls -lh assets/lambda/revalidator/bootstrap
echo ""
echo "Next steps:"
echo "  1. Build CLI: go build -o nextdeploy ./cli/main.go"
echo "  2. Test deployment: ./nextdeploy build && ./nextdeploy ship"
