# Homebrew formula for the Coder CLI (tjcoder/cli).
#
# Install with:
#   brew tap tjcoder-labs/tap
#   brew install coder
#
# The formula downloads the platform-specific prebuilt release tarball
# from GitHub and installs the `coder` binary. The sha256 values below are
# for v0.9.272; regenerate them when bumping VERSION_TAG.
class Coder < Formula
  desc "Terminal-first AI coding assistant (TJ Coder / AI Labs)"
  homepage "https://github.com/tjcoder-labs/cli"
  version "0.9.272"
  license "MIT"

  VERSION_TAG = "v0.9.272"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_arm64.tar.gz"
      sha256 "27c9dd2c2c31f90b37350e7bf876e3620960f0e6142675aeafccaf3d08d24fcc"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_amd64.tar.gz"
      sha256 "9d801737d9a65cf166508699b2f918b296df842e0bcf5c6402ef91f66ae38ff1"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_arm64.tar.gz"
      sha256 "86955809eef0ebf832b8c8082ffc15948df9b5f07a20be74e09424404cbade84"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_amd64.tar.gz"
      sha256 "bde81b399f129703b92e35718e2387479267a962711cab2ca8dc9b695c4361ce"
    end
  end

  head "https://github.com/tjcoder-labs/cli.git", branch: "main"

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "make", "build"
      bin.install "bin/coder"
    else
      bin.install "coder"
    end
  end

  test do
    assert_match "coder", shell_output("#{bin}/coder --version 2>&1")
  end
end
