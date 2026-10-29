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

A Silo build that asks an artwork plugin for images by other providers' IDs.
Two open server changes do that, each with its own capability key:

- `lookup_images`, in
  [Silo-Server/silo-server#3145](https://github.com/Silo-Server/silo-server/pull/3145);
- `image_lookup_provider_ids`, in
  [Silo-Server/silo-server#2259](https://github.com/Silo-Server/silo-server/pull/2259).

The manifest declares both, so the plugin works with whichever lands. On a
build with neither, Silo never asks the plugin for artwork, so it installs but
does nothing.

## Network access and credentials

- The plugin makes one kind of request:
  `GET https://webservice.fanart.tv/v3/movies/{id}` or `.../v3/tv/{id}`. It
  sends only the title's TMDB, IMDb or TVDB ID. It makes no other outbound
  connections and never writes anywhere.
- The keys travel in the `api-key` and `client-key` request headers, never in
  a URL, so they don't end up in Silo's logs or in an image address.
- The image URLs it returns point at `https://assets.fanart.tv/`. Silo's image
  cache downloads them, not the plugin.
- Answers are kept in memory for 30 minutes, so one refresh, and an
  administrator paging through a show's seasons, cost one request per title.
- Release builds carry a fanart.tv project key, the way fanart.tv intends
  project keys to be used and the way the TMDB plugin carries its TMDB key. A
  key built into a binary can be read out of it. An administrator's own
  project key or personal client key goes in the plugin's secret settings.

## Support

Report problems in this repository's
[issues](https://github.com/blurbery/silo-plugin-metadata-fanarttv/issues), and
security vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

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

fanart.tv's API has no language filter, so the plugin applies the one the TMDB
plugin asks TMDB for: it returns only artwork in the library's metadata
language, English as the fallback, and artwork with no language. Artwork in
any other language never reaches the image picker.

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
`cmd/catalog`). When the `SILO_PLUGINS_DISPATCH_TOKEN` secret is set, the
release also notifies the approved community catalogue
(`Silo-Community/silo-plugins`).

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
