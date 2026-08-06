# Curator for Lightroom Classic

This directory contains the Curator Publish Service plugin. Published
collection sets become parent galleries, published collections become Curator
galleries, and Lightroom keeps their photos synchronized.

## Install and connect

1. Open **Settings → Publishing** in Curator and choose **Create publishing
   token**. Copy the token while it is visible.
2. In Lightroom Classic, open Plug-in Manager and add `Curator.lrplugin`.
3. Create a Curator Publish Service and enter the admin server URL and
   publishing token.
4. Use **Test connection**, then publish JPEGs normally.

Creating a published collection or collection set synchronizes the Curator
hierarchy, including empty nodes. New galleries use the defaults under
Curator's Site settings. Enable **Publish immediately** to make Lightroom
collections visible on the next static build, and **EXIF** to show camera
details in their lightboxes by default.

For headless setup, `curator create-publish-token -content /path/to/content`
rotates the same credential and prints it once. Restart `curator serve` after
using the CLI command while the server is running. UI rotation takes effect
immediately.

The server stores only the token hash. The plugin stores the bearer token in
Lightroom's encrypted, OS-backed password vault; it migrates and clears tokens
saved by older plugin versions. Deleting the Publish Service clears its vault
entry without deleting synchronized Curator content.

## Current contract

- `GET /api/v1/` discovers API version and capabilities.
- `GET /api/v1/galleries` lists galleries.
- `POST /api/v1/galleries` creates a gallery.
- `POST /api/v1/galleries/{id}/photos` accepts a multipart JPEG or PNG, optional
  XMP sidecar, and caption.
- `PUT /api/v1/sync/galleries/{externalID}` creates or updates one Lightroom
   collection or collection set.
- `POST /api/v1/sync/galleries/{id}/photos/{externalID}` creates or replaces a
   published photo while preserving its Curator item ID.
- `PUT /api/v1/sync/galleries/{id}/order` applies Lightroom's user order.
- `DELETE /api/v1/sync/photos/{id}` and
   `DELETE /api/v1/sync/galleries/{id}` remove Lightroom-owned content.
- `POST /api/v1/sync/build` queues a static build; overlapping requests are
   coalesced.

All requests require `Authorization: Bearer <token>`. External identities are
namespaced by publish-service connection, so multiple Lightroom catalogs can
publish to the same Curator instance without ID collisions. The API refuses to
delete ordinary content that is not owned by the Lightroom synchronization.
Caption changes trigger Lightroom republish. Successful publishes record public
gallery and lightbox URLs when Curator has a Base URL and the gallery is public.
Transport activity is written to Lightroom's `Curator.log` without credentials.

## Lens keywords

For manual or adapted lenses, create this Lightroom keyword hierarchy:

```text
Curator Lens
└── Voigtlander 40mm f/1.2
```

Assign exactly one direct child of the top-level `Curator Lens` keyword to a
photo. The child name becomes that photo's lens name in Curator. The plugin
reads Lightroom's catalog keywords, so **Write Keywords as Lightroom
Hierarchy** has no effect on this synchronization. Mark the root keyword
**Exclude on Export** only if you do not want the hierarchy embedded in the
JPEG. Removing the lens keyword and republishing clears the Lightroom value.
Assigning multiple direct children fails that photo's publish rather than
choosing one unpredictably.

Lens precedence is a per-photo override entered in Curator, the explicit
Curator Lens keyword, embedded EXIF, direct XMP sidecar lens, camera mapping,
then the optional Lightroom lens-profile fallback. Lightroom marks photos for
republish after keyword changes. See the
[lens metadata guide](../docs/lens-metadata.md) for Curator-side overrides and
resolution settings.
