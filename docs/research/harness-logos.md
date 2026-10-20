# Official harness logos and usage rules

Research for [#11](https://github.com/jbuffin/admiral/issues/11). Question: which official logo file should each harness flag use, and what do the vendors' usage rules say about sizing, clear space, colors, attribution and use in a third-party app?

Checked 2026-10-17 against the vendors' own pages and asset kits. The logo files are linked here, not committed.

## The flag the logos go into

`designs/assets/flag.svg` is a 40×28 swallowtail. Per its comment, the logo goes in the left two thirds, so it gets roughly a 24×20 box at full size. On ships the flag renders at about 29×15 px, so the logo sits at about 10–12 px tall. The prototype fills the flag with a per-harness color (`designs/tokens.json` → `harness`): Claude `#FF7A59`, Copilot `#7A5AF0`, Codex `#1F3B57`.

## Summary

| Harness | File to use | Format | Colors you may use | Third-party use |
| --- | --- | --- | --- | --- |
| Claude Code | **Claude Spark - Clay** from Anthropic's press kit | SVG 94×94, single fill `#D97757` | As supplied, with no recoloring | Plain-text "runs Claude Code" is allowed. Any logo use needs Anthropic's written permission. |
| Copilot CLI | **Copilot_Icon_White** / **Copilot_Icon_Black** from GitHub's logo kit | SVG 96×96 (also PNG, PDF; `_Clearspace` variants 128×128) | White or black, highest contrast | Allowed to show that a project integrates with GitHub, less prominently than your own brand, with no modification. Otherwise prior written permission is needed. |
| Codex CLI | **OpenAI Blossom** (OpenAI's symbol) from openai.com/brand. I found no separate Codex mark. | Not confirmed; see the caveat below | Approved color formats only | Only where it directly relates to OpenAI services, never more prominent than your own brand, and never altered. Permission requests go to <partnercomms@openai.com>. |

## Claude Code (Anthropic)

**Files.** The newsroom's "Download press kit" link ([anthropic.com/news](https://www.anthropic.com/news)) is <https://www.anthropic.com/press-kit>, which redirects to a 26 MB zip on `www-cdn.anthropic.com`. Under `Anthropic logos/Claude logos/` it contains:

- `3 Claude Spark/SVG/Claude Spark - Clay.svg`: the spark symbol alone, 94×94 viewBox, one path filled `#D97757` (Clay). Also as PNG. **This is the one for the flag.** It is the only square, symbol-only Claude mark and the only one legible at 10–20 px.
- `4 Claude icon/SVG/ClaudeIcon-Square.svg` and `ClaudeIcon-Rounded.svg`: the app icon, a 128×128 tile with a Clay gradient and an ivory (`#FAF9F5`) spark. Use this if the flag needs a self-contained tile.
- `2 Claude Code logo/SVG/Claude Code logo - {Slate,Ivory,One-color}.svg`: the Claude Code lockup. Its viewBox is 14552×2000 (about 7:1), far too wide for the flag.
- `1 Claude logo/`: the Claude wordmark logo (Slate, Ivory, One-color).

There is no Claude Code-specific symbol. The Claude Code logo is a wide lockup, so the spark is the closest square mark.

**Rules** ([Trademark guidelines](https://www.anthropic.com/legal/trademark-guidelines)):

- You may use the marks "only as specifically permitted by us and only in materials we approve beforehand", and permission can be withdrawn at any time.
- Changing color, font or proportion is not allowed. The spark has to stay Clay and keep its aspect ratio.
- Leave "a reasonable space" between the mark and other graphic or text elements. No number is given; Anthropic says it supplies size, pixel and spacing requirements together with the images it approves.
- Don't place the mark on a background that interferes with its readability or display.
- Don't add a ™ symbol.
- Don't imply sponsorship or endorsement.
- Requests go to <marketing@anthropic.com> (the page addresses this to companies with an existing business relationship).

**Claude Code specifically** ([Legal and compliance, "Using the Claude Code name and logo"](https://code.claude.com/docs/en/legal-and-compliance)): you may say in plain text that a product runs Claude Code. You may not use the Claude Code or Anthropic names or logos in your own product name or logo, or in a way that suggests Anthropic built, endorses or partners with the product. "Any other use of Anthropic's names or logos is governed by our Trademark Guidelines and requires our written permission."

**Constraint for Admiral:** showing the spark on a flag is a logo use that needs Anthropic's written permission. The "Claude Code" name in plain text, such as the bridge header's `harness · fleet · skill` line, is explicitly fine. Clay `#D97757` on the placeholder coral `#FF7A59` has almost no contrast, which breaks the background rule. The Claude flag would need an ivory or white field behind the spark, or the `ClaudeIcon-Square` tile.

## GitHub Copilot CLI (GitHub)

**Files.** "Download the logo files" on [brand.github.com/foundations/logo](https://brand.github.com/foundations/logo) is <https://brand.github.com/GitHub_Logos.zip> (about 0.5 MB). It contains SVG, PNG and PDF versions of:

- `Copilot_Icon_Black.svg` / `Copilot_Icon_White.svg`: the Copilot icon alone, 96×96. **These are the ones for the flag.** The black version uses black and white fills.
- `Copilot_Icon_{Black,White}_Clearspace.svg`: the same icon on a 128×128 canvas. That works out to 16 units of required padding on each side, or 1/6 of the icon's width.
- `Copilot_Lockup_*` and `GitHub_Copilot_Lockup_*`: horizontal lockups, too wide for the flag.

**The standalone icon is deprecated as a product logo.** [brand.github.com/brand-identity/copilot](https://brand.github.com/brand-identity/copilot) says: "Beginning in 2025, GitHub Copilot no longer has a standalone logo that heros the Copilot icon". GitHub now leads with the "GitHub Copilot" lockup. The icon still ships in the official kit, but the guidance treats the lockup as the product's mark. The same page gives Copilot Purple as `#8534F3`.

**Rules** ([brand.github.com/foundations/logo](https://brand.github.com/foundations/logo), logo and legal sections):

- Colors: the logos "should only appear in white, black, or in few cases grey or green", and "When in doubt, use the highest contrast option."
- Don't rearrange, recolor, add shadows or gradients, compress, distort, skew or stretch the logo. Don't put it on busy or low-contrast backgrounds.
- Allowed: "Use a permitted GitHub logo to inform others that your project integrates with GitHub", provided it is less prominent than your own name or logo.
- Not allowed: implying you are GitHub or endorsed by it, using a GitHub logo as your own product's icon, or modifying it, including "changing the color, dimensions". The page also says "Do not use GitHub trademarks, logos, or artwork without GitHub's prior written permission". The page doesn't say which logos count as "permitted". Contact: <https://github.com/contact>.
- Minimum size: none stated. Clear space: shown only as an image, but the `_Clearspace` files encode it.

**Constraint for Admiral:** use the white icon on a dark flag or the black icon on a light flag. Don't tint it purple; the placeholder `#7A5AF0` flag with the white icon is fine. Keep the 1/6 padding, which at a 20-unit box leaves the icon about 15 units wide. Because the icon is deprecated as Copilot's standalone logo, GitHub could object to it standing in for Copilot. Its "integrates with GitHub" allowance is the closest fit.

## Codex CLI (OpenAI)

**Files.** OpenAI's [Design Guidelines at openai.com/brand](https://openai.com/brand/) say they are where you "download official logos, with guidance for using our wordmarks, typography, trademarks, and brand assets." **Caveat:** that page and OpenAI's policy pages returned HTTP 403 to every automated fetch (Cloudflare). The Internet Archive was offline and the browser extension wasn't connected. The rules below come from search-engine extracts of openai.com's own pages. I could not confirm the download URLs or file formats. Someone needs to open openai.com/brand in a browser and record the exact file.

- I found no Codex-specific logo on any OpenAI-owned page. The `openai/codex` repo carries only a CLI screenshot (`.github/codex-cli-splash.png`) and small skill sample icons. The mark to use is the **OpenAI Blossom**, OpenAI's symbol. The wordmark is too wide.

**Rules** (openai.com/brand, via search extracts):

- "Use the logo only when it directly relates to OpenAI services. Follow OpenAI's style guides and usage terms. Use the logo exactly as provided and acknowledge that it belongs to OpenAI."
- Don't "use the logo more prominently than your own or in unrelated contexts". Don't put it on merchandise, alter it, or fold it into your own branding.
- The Blossom has prescribed clear space ("DO use the established spacing rules"). I could not get the number. The wordmark has its own clear space and is not used together with the Blossom.
- Logos must appear "in their approved color formats" without distortion. Co-branded material needs approval from both brands. Don't pair the Blossom with the partnership lockup.
- Permission: "permission requests for the use of our logos … please contact <partnercomms@openai.com>"; legal questions go to <legal@openai.com>.
- [App Developer Terms](https://openai.com/policies/developer-apps-terms/) (for apps built on OpenAI's app platform; not Admiral's case, but they show OpenAI's stance): OpenAI Assets are licensed "solely to promote your Apps", and you must not suggest that OpenAI created, supports, certifies or endorses the app.

**Constraint for Admiral:** an unaltered Blossom in an approved color (black or white), with its prescribed clear space, on a contrasting field. A white Blossom on the placeholder navy `#1F3B57` is plausible. Pointing at a Codex ship is a direct relation to an OpenAI service. Acknowledging ownership means a trademark notice somewhere, such as an About or credits screen.

## What this means for the flags

1. **Every logo goes in unaltered: no recoloring, no clipping into the swallowtail, original aspect ratio, and the vendor's padding.** Each flag needs a field color that contrasts with the logo's fixed colors: ivory or white for Claude's Clay spark, and dark for the white Copilot icon and white Blossom.
2. **Attribution:** add a trademark notice (in About or a footer) saying that Claude Code is Anthropic's, Copilot GitHub's and Codex OpenAI's, and that Admiral is not affiliated with or endorsed by any of them. All three vendors forbid implying endorsement. OpenAI also asks for an ownership acknowledgment. Anthropic's guidelines say not to add ™ to its marks.
3. **Prominence:** the logos must stay smaller than Admiral's own mark. Flags already are.
4. **Permission:** Anthropic requires written permission for any logo use beyond plain-text naming. GitHub requires prior written permission outside its "integrates with GitHub" allowance. OpenAI routes logo permission requests to <partnercomms@openai.com>. For a personal tool running locally the exposure is low. Before Admiral is shared with other people, which is a standing preference, either get permission or keep the official files out of the distributed package: the engineer supplies them, or Admiral fetches them from the vendor URLs above at install. Keep the lettered fallback (Cl, Co, Cx) for when no file is present.

## Sources

- Anthropic, Trademark guidelines: <https://www.anthropic.com/legal/trademark-guidelines>
- Anthropic, Claude Code legal and compliance ("Using the Claude Code name and logo"): <https://code.claude.com/docs/en/legal-and-compliance>
- Anthropic, Newsroom (press kit link): <https://www.anthropic.com/news>; press kit: <https://www.anthropic.com/press-kit>
- GitHub, Logo guidelines and legal: <https://brand.github.com/foundations/logo>; logo kit: <https://brand.github.com/GitHub_Logos.zip>
- GitHub, Copilot brand identity: <https://brand.github.com/brand-identity/copilot>
- OpenAI, Design Guidelines: <https://openai.com/brand/> (blocked to automated fetch; read through search extracts)
- OpenAI, App Developer Terms: <https://openai.com/policies/developer-apps-terms/> (same)
- OpenAI, Codex repository: <https://github.com/openai/codex>
