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

1. Sign in to fanart.tv and create an API key. A personal client key is
   optional and shows newly added artwork sooner.
2. Install the plugin in Silo and enter the key in its settings.
3. Installing adds **fanart.tv** to every library's provider chain below TMDB.
   There it adds choices to the image picker and fills artwork TMDB does not
   have, such as clear art, disc art, banners and thumbs.
4. To prefer fanart.tv's posters, backgrounds and logos, move it above TMDB in a
   library's chain, then refresh the library's metadata.

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
Pushing a `v*` tag that matches `manifest.json`'s version publishes a release.

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
