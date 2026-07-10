# Source-build formula prepared for a future Homebrew/homebrew-core
# submission (Formula/a/ankra.rb). Not wired into any automation: the vendor
# tap at ankraio/homebrew-tap (rendered from packaging/homebrew/ankra.rb.tmpl)
# remains the live brew channel until the repo meets homebrew-core's
# notability bar (>=75 stars, >=30 forks, or >=30 watchers). Bump url/sha256
# to the latest stable tag before submitting.
class Ankra < Formula
  desc "Command-line interface for the Ankra Kubernetes platform"
  homepage "https://ankra.io"
  url "https://github.com/ankraio/ankra-cli/archive/refs/tags/v0.6.0.tar.gz"
  sha256 "757a2998e57e9de32430bcf37308659aaf44dc7e2b9b497466df16e79370d8af"
  license "Apache-2.0"
  head "https://github.com/ankraio/ankra-cli.git", branch: "master"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=v#{version}")
    generate_completions_from_executable(bin/"ankra", "completion")
  end

  test do
    assert_match "ankra version v#{version}", shell_output("#{bin}/ankra --version")

    output = shell_output("#{bin}/ankra cluster list 2>&1", 6)
    assert_match "not logged in", output
  end
end
