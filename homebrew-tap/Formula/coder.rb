# Homebrew formula for the Coder CLI (tjcoder/cli).
#
# Install with:
#   brew tap tjcoder-labs/tap
#   brew install coder
#
# The formula downloads the platform-specific prebuilt release tarball
# from GitHub and installs the `coder` binary. The sha256 values below are
# for v0.9.251; regenerate them when bumping VERSION_TAG.
class Coder < Formula
  desc "Terminal-first AI coding assistant (TJ Coder / AI Labs)"
  homepage "https://github.com/tjcoder-labs/cli"
  version "0.9.251"
  license "MIT"

  VERSION_TAG = "v0.9.251"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_arm64.tar.gz"
      sha256 "e55052cfb85f7b7a50246bede39150980b37932c26e77357507a6ec60ffd07ac"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_amd64.tar.gz"
      sha256 "977e54822f927264431ecd3f2c86e6d113d82912f03adccb70e6434bee3948df"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_arm64.tar.gz"
      sha256 "9eacc127507b1f39d005d00fbc2d7010e0e36d854944860b4d05582bbec7ef65"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_amd64.tar.gz"
      sha256 "772fba5434286d00c7fa19e97e83d5aaee7b4aae3d9106295e7ef0808149ac24"
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
