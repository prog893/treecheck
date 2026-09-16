# Template for Formula/treecheck.rb in prog893/homebrew-tap, for the Go
# implementation (2.x). It is not used by any build in this repository: the tap
# is its own repository and is updated by hand at release time. Copy this file
# there, with the tag set to the release being published.
class Treecheck < Formula
  desc "Verify files against SHA-256 sidecars to catch silent corruption"
  homepage "https://github.com/prog893/treecheck"
  # No `version` line: Homebrew scans it from the tag, and declaring both is
  # flagged as redundant. The tag is written out rather than interpolated as
  # "v#{version}", since style autocorrect sorts `url` above `version`, at which
  # point the interpolation resolves to a bare "v" and the clone fails with
  # "Remote branch v not found in upstream origin".
  url "https://github.com/prog893/treecheck.git", tag: "v2.0.0"
  license "MIT"

  head "https://github.com/prog893/treecheck.git", branch: "main"

  # Built from source: the Go standard library covers everything, so the only
  # dependency is the compiler, and only at build time.
  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w")
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/treecheck --version")

    # Verify the actual job end to end, not just that the binary runs. A file
    # whose contents no longer match its sidecar must be reported and must exit
    # non-zero, since a checker that cannot fail is worse than no checker.
    (testpath/"good.txt").write "intact"
    (testpath/"good.txt.sha256").write "#{Digest::SHA256.hexdigest("intact")}\n"
    assert_match "Verified:        1", shell_output("#{bin}/treecheck #{testpath}")

    # File.write rather than Pathname#write: Homebrew's Pathname refuses to
    # overwrite an existing file, and corrupting this one in place is the point.
    File.write(testpath/"good.txt", "changed")
    output = shell_output("#{bin}/treecheck #{testpath}", 1)

    # Assert the verdict line, its detail and the counter separately. Each
    # covers a different way the report has silently gone wrong before: the
    # token is the outcome, the recorded digest proves the sidecar was read
    # back rather than assumed, and the counter is what a caller scripts on.
    assert_match "MISMATCH", output
    assert_match "recorded", output
    assert_match "Mismatched:      1", output
  end
end
