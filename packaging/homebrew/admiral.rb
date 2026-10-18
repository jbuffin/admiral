# Formula for the jbuffin/admiral tap (the jbuffin/homebrew-admiral repo).
# The release process fills in `url` and `sha256` for each release; see
# "Cutting a release" in the README.
class Admiral < Formula
  desc "Visual dashboard that launches coding-agent sessions and lets you watch them"
  homepage "https://github.com/jbuffin/admiral"
  url "https://github.com/jbuffin/admiral/releases/download/vVERSION/admiral-VERSION-darwin-arm64.tar.gz"
  sha256 "SHA256_OF_THE_TARBALL"
  version "VERSION"

  depends_on :macos
  depends_on arch: :arm64
  depends_on "python@3.15"
  depends_on "tmux"

  def install
    bin.install "admiral"
    (share/"admiral/plugins").install "plugins/freeform"
    prefix.install "THIRD_PARTY_NOTICES"
  end

  def caveats
    <<~EOS
      Set up your first fleet:
        admiral setup

      Start Admiral at login and keep it running:
        admiral service install

      After `brew upgrade admiral`, restart the running service:
        launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral
    EOS
  end

  test do
    assert_match "admiral #{version}", shell_output("#{bin}/admiral --version")
  end
end
