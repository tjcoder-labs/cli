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
  desc "Open-source terminal AI coding assistant and LLM agent CLI. A keyboard-driven Go TUI with 7 specialized agents, 30+ tools, and a live reasoning pane."
  homepage "https://github.com/tjcoder-labs/cli"
  version "0.9.273"
  license "MIT"

  VERSION_TAG = "v0.9.273"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_arm64.tar.gz"
      sha256 "707a587f4b895eba57c175b74035f377eb396ddae7704808de9e2dbcfa15177c"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_darwin_amd64.tar.gz"
      sha256 "bf145d950832d1ec45600f613f318b6bf0f57266a50333e76a21ddf7bc40f09e"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_arm64.tar.gz"
      sha256 "277062bf9ddc1d4944bfdda3981718b9e52613f7d0704319a69fea0f48e22ff9"
    else
      url "https://github.com/tjcoder-labs/cli/releases/download/#{VERSION_TAG}/coder_#{VERSION_TAG}_linux_amd64.tar.gz"
      sha256 "8f9a6ff547eb1616ce6f5189cc2f845185467d0f035edc0030bfbb4ec6fe0fa5"
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
