# fanart.tv artwork for Silo

A Silo metadata provider plugin that adds [fanart.tv](https://fanart.tv) artwork
to movie and series libraries: posters, backgrounds, HD logos, clear art, disc
art, banners and thumbs, plus season posters in the image picker.

It supplies artwork only. It never identifies a title and fills no metadata.
It looks titles up by the IDs a primary provider such as TMDB already resolved:

- Movies by TMDB ID, or IMDb ID when there is no TMDB ID.
- Series by TVDB ID, the only ID fanart.tv files shows under. A series with no
  TVDB ID gets no fanart.tv artwork.

## Requirements

A Silo build whose metadata service honours the `lookup_images` capability
flag. It lets an artwork source with no IDs of its own be asked for images by
its `lookup_provider_ids`. On an older build Silo never asks this plugin for
artwork, so it installs but does nothing.

## Setup

No setup is needed. Release builds carry a fanart.tv project key, set from the
`FANARTTV_API_KEY` Actions secret, and the release workflow refuses to publish
without it. The plugin settings take an optional personal client key, which
shows newly added artwork sooner, and an optional project key that replaces
the built-in one.

**fanart.tv** sits below TMDB in each library's provider chain. There it adds
choices to the image picker and fills artwork TMDB does not have, such as clear
art, disc art, banners and thumbs. To prefer fanart.tv's posters, backgrounds
and logos, move it above TMDB in a library's chain, then refresh the library's
metadata.

## Installing by default

Each release publishes `catalog.json`, a one-plugin Silo catalogue, so a Silo
build can add
`https://github.com/blurbery/silo-plugin-metadata-fanarttv/releases/latest/download/catalog.json`
as a plugin repository and install and update the plugin from it.

## How artwork is chosen

Silo picks artwork per language in provider-chain order, and within one
provider by rating. The plugin turns fanart.tv's likes into that rating:

- Every image rates above zero, so a refresh can replace stored artwork with it.
- An HD logo or HD clear art always outranks the older SD version.
- Posters tagged with a language count as having text. fanart.tv files a
  textless poster under no language.
- Backgrounds are offered with no language, since fanart.tv's rules keep them
  free of text.

Season posters are offered when an administrator opens a season's image
picker. They are not applied automatically during a refresh.

When fanart.tv cannot answer (no key, a rejected key, an outage) the plugin
answers with no artwork rather than an error, and logs the cause once, so a
library refresh carries on with the other providers.

## Development

```sh
make test
make build
```

`make build-all` cross-compiles the Linux and macOS binaries into `dist/`.
Pushing a `v*` tag that matches `manifest.json`'s version publishes a release
with the binaries, `checksums.txt` and `catalog.json` (written by
`cmd/catalog`).

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
