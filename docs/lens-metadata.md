# Lens metadata

Curator keeps lens values from each metadata source separately and resolves one
effective lens name for galleries, lightboxes, and lens browse pages.

## Edit one photo

Open a photo in the admin and select **Metadata → Manual lens override**. Choose
a lens name already used in the library or enter a new one. Existing names are
suggested by manual use and then by frequency, making spelling and naming
consistent across photos.

Clear the field to remove the override and return to automatic resolution.
Manual overrides remain attached to the photo when source metadata is refreshed
or its image is replaced.

## Resolution order

Curator uses the first non-empty value in this order:

1. Per-photo manual override entered in Curator
2. Direct child of Lightroom's `Curator Lens` keyword
3. Embedded EXIF lens name
4. Adjacent XMP sidecar lens name
5. Configured camera-to-lens mapping
6. Lightroom XMP lens profile, when enabled as a fallback

Camera mappings and the Lightroom fallback are configured under
**Settings → Metadata**. Policy changes take effect on the next build and do not
require source metadata to be refreshed.

## Lightroom keywords

The Lightroom plugin can assign a lens by placing exactly one direct child below
the `Curator Lens` keyword on a photo. Republish the photo after changing the
keyword. A manual override entered in Curator remains authoritative until it is
cleared.

See the [Lightroom setup guide](../lightroom/README.md#lens-keywords) for the
keyword hierarchy and publishing behavior.

## Refresh source metadata

Use **Refresh metadata** after embedded EXIF or adjacent XMP files change. A
refresh rereads source facts but does not replace Curator's manual override.