# Camera and lens metadata

Curator keeps the imported camera name separate from an optional manual camera
override. This is useful for scanned film, where EXIF may identify a scanner
such as `Frontier` instead of the camera that exposed the negative.

## Camera overrides

Open a photo in the admin and select **Metadata → Manual camera override**.
Choose a camera already used in the library or enter a new name. Galleries,
lightboxes, camera browse pages, and camera-based lens mappings use the manual
name while the override is present.

The imported EXIF camera remains visible in the editor for provenance. Clearing
the override restores that imported value. Camera overrides remain attached to
the photo when source metadata is refreshed or its image is replaced.

## Lens metadata

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
6. XMP lens metadata, when enabled as a fallback

After choosing that value, Curator applies an exact lens-name mapping when one
is configured. This final normalization step is source-neutral: it works on
names from manual overrides, Lightroom keywords, EXIF, sidecars, camera
mappings, and XMP fallback metadata.

Camera mappings and the XMP fallback are configured under
**Settings → Metadata**. To enable the fallback, select **Use XMP lens metadata
as a fallback**, save the metadata settings, and publish again. Policy
changes take effect on the next build and do not require source metadata to be
refreshed. A mapping applies to every stored photo whose effective camera name
matches it; Curator does not need to reopen those image files.

Available XMP lens metadata means Curator detected either a direct lens name
(`aux:Lens` or `exifEX:LensModel`) or an Adobe Camera Raw lens profile name
(`crs:LensProfileName`) in embedded or sidecar XMP. Detection alone does not
make that name active: the fallback setting must be enabled, and any value
earlier in the resolution order still wins. A camera-to-lens mapping is
therefore optional when the fallback is enabled, but adding one overrides the
XMP value.

## Normalize lens names

Under **Settings → Metadata → Lens-name mappings**, map an existing lens name to
the canonical name Curator should display. For example:

- `45.0 mm f/2.8` → `Nikkor 45mm f/2.8P AI-s`
- `Nikkor 28mm f/3.5 AI-s` → `Nikkor 28mm f/3.5 AI`

Names already found in the library are suggested in both fields. Matching is
exact, and mappings are applied once rather than chained. The source metadata
and per-photo overrides remain unchanged, so removing a mapping restores the
name selected by normal metadata resolution.

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
Adding a mapping or enabling the XMP lens fallback does not require a
refresh; publish again to apply the new resolution policy.
