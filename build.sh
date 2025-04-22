#!/usr/bin/env bash
set -euo pipefail

DIST_DIR="dist"
PLATFORMS=("linux" "darwin" "windows")
ARCHS=("amd64" "arm64")
APPS=(
  "better-docs-server:./"
  "swagger-fetcher:./swagger-fetcher"
)

check_go() {
  if ! command -v go &>/dev/null; then
    echo >&2 "ERROR: Go toolchain not found. Please install Go."
    if [ -f /etc/os-release ]; then . /etc/os-release; case "$ID" in
        arch)      echo >&2 "  sudo pacman -S go";;
        ubuntu|debian) echo >&2 "  sudo apt update && sudo apt install -y golang";;
        fedora)    echo >&2 "  sudo dnf install -y golang";;
        centos)    echo >&2 "  sudo yum install -y golang";;
        gentoo)    echo >&2 "  sudo emerge --ask dev-lang/go";;
      esac; fi
    exit 1
  fi
}

check_java() {
  if ! command -v java &>/dev/null; then
    echo >&2 "ERROR: Java not found. Please install Java 21."
    exit 1
  fi

  JAVA_VERSION=$(java -version 2>&1 | awk -F '"' '/version/ {print $2}')
  JAVA_MAJOR=${JAVA_VERSION%%.*}
  if [ "$JAVA_MAJOR" != "21" ]; then
    echo >&2 "ERROR: Java 21 is required, but found version $JAVA_VERSION"
    exit 1
  fi
}

get_version() {
  if git rev-parse --git-dir &>/dev/null; then
    if tag=$(git describe --tags --exact-match HEAD 2>/dev/null); then
      echo "$tag"
    elif commit=$(git rev-parse --short HEAD 2>/dev/null); then
      echo "$commit"
    else
      echo "unknown"
    fi
  else
    echo "unknown"
  fi
}

prepare_dist() {
  echo "Preparing $DIST_DIR..."
  rm -rf "$DIST_DIR"
  mkdir -p "$DIST_DIR"
}

build_swagger_converter() {
  echo "Building swagger-converter-cli shadowJar…"
  (cd swagger-converter-cli && ./gradlew shadowJar)
}

build_app() {
  local name="$1" dir="$2" os="$3" arch="$4" version="$5" ext=""
  [ "$os" == "windows" ] && ext=".exe"
  echo "Building $name for $os/$arch..."
  local root="$(pwd)"
  (cd "$dir" && \
    GOOS=$os GOARCH=$arch go build -ldflags "-X main.version=$version" \
      -o "$root/$DIST_DIR/${name}-${os}-${arch}${ext}" .)
}

package_app() {
  local os="$1"
  local arch="$2"
  local version="$3"
  local base_dir="${os}-${arch}-${version}"
  local pkg_dir="$DIST_DIR/$base_dir"
  mkdir -p "$pkg_dir"

  for entry in "${APPS[@]}"; do
    IFS=":" read -r app _ <<< "$entry"
    local ext=""
    [ "$os" == "windows" ] && ext=".exe"
    cp "$DIST_DIR/${app}-${os}-${arch}${ext}" "$pkg_dir/"
  done

  cp -r static "$pkg_dir/" 2>/dev/null || true
  cp -r api "$pkg_dir/" 2>/dev/null || true
  cp -r specs.json "$pkg_dir/" 2>/dev/null || true
  cp -r .bleveIndexes "$pkg_dir/" 2>/dev/null || true

  if [ "$os" == "windows" ]; then
    cp start.cmd "$pkg_dir/" 2>/dev/null || true
  else
    cp start.sh "$pkg_dir/" 2>/dev/null || true
  fi

  JAR_SRC=$(ls swagger-converter-cli/build/libs/swagger-converter-cli*.jar 2>/dev/null | head -n1)
  if [ -n "$JAR_SRC" ]; then
    echo "Including swagger-convert.jar from $(basename "$JAR_SRC") into $base_dir"
    cp "$JAR_SRC" "$pkg_dir/swagger-convert.jar"
  else
    echo "Warning: no swagger-converter-cli JAR found; skipping swagger-convert.jar"
  fi

  cp index.html "$pkg_dir/" 2>/dev/null || true

  pushd "$DIST_DIR" > /dev/null
    if [ "$os" == "windows" ]; then
      local archive="${base_dir}.zip"
      echo "Creating $archive"
      zip -r "$archive" "$base_dir"
      echo "Contents of $archive:"; unzip -l "$archive"
    else
      local archive="${base_dir}.tar.gz"
      echo "Creating $archive"
      tar czf "$archive" "$base_dir"
      echo "Contents of $archive:"; tar tzvf "$archive"
    fi
  popd > /dev/null

  rm -rf "$pkg_dir"
  for entry in "${APPS[@]}"; do
    IFS=":" read -r app _ <<< "$entry"
    local ext=""
    [ "$os" == "windows" ] && ext=".exe"
    rm -f "$DIST_DIR/${app}-${os}-${arch}${ext}"
  done
}

check_go
check_java
VERSION=$(get_version)
echo "Version: $VERSION"
prepare_dist
build_swagger_converter

for os in "${PLATFORMS[@]}"; do
  for arch in "${ARCHS[@]}"; do
    for entry in "${APPS[@]}"; do
      name="${entry%%:*}"
      dir="${entry##*:}"
      build_app "$name" "$dir" "$os" "$arch" "$VERSION"
    done
    package_app "$os" "$arch" "$VERSION"
  done
done

echo "All packages are in $DIST_DIR."