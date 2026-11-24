# image-management Specification

## Purpose

Defines how users view and operate on Docker images in drydock: listing with usage information, deletion, inspection, pulling new images, pruning dangling images, and navigating to the containers that use an image.

## Requirements

### Requirement: Image list
The images view SHALL list images with the columns REPOSITORY:TAG, ID (short), SIZE (human-readable), CREATED (relative age), and IN USE (number of containers, running or stopped, created from the image), with one row per tag and untagged images shown as `<none>:<none>`.

#### Scenario: Tagged image in use
- **WHEN** image `nginx:1.27` is used by containers `web` (running) and `old` (stopped)
- **THEN** its row shows IN USE as 2

#### Scenario: Image with multiple tags
- **WHEN** one image ID is tagged both `app:latest` and `app:1.0`
- **THEN** two rows are shown, one per tag, with the same ID

### Requirement: Toggle dangling images
The images view SHALL hide dangling images (images with no tags) by default and show them when the user presses `a`, indicating the current mode in the header.

#### Scenario: Show dangling images
- **WHEN** the user presses `a` in the images view
- **THEN** dangling images appear as `<none>:<none>` rows and the header indicates "all"

### Requirement: Delete image
The images view SHALL delete the selected image reference after confirmation when the user presses `d`. If the image is in use and the selected reference is its only tag, the confirmation SHALL name the containers using it and warn that removal will fail; if other tags remain, it SHALL state that only the tag is removed. Removal is never forced.

#### Scenario: Delete unused image
- **WHEN** the user presses `d` on unused image `alpine:3.20` and confirms
- **THEN** the reference is removed and disappears from the list

#### Scenario: Delete a tag shared by another tag
- **WHEN** the image ID has tags `app:latest` and `app:1.0` and the user deletes `app:1.0`
- **THEN** only the `app:1.0` tag is removed and `app:latest` remains listed

#### Scenario: Delete in-use image
- **WHEN** the user presses `d` on `nginx:1.27`, which is used by `web`
- **THEN** the confirmation lists `web` as using it; if confirmed, the engine's refusal is shown as an error and the image remains

#### Scenario: Untag an in-use image with other tags
- **WHEN** `nginx:1.27` and `nginx:latest` share an image ID used by `web`, and the user presses `d` on `nginx:1.27`
- **THEN** the confirmation states that only the `nginx:1.27` tag will be removed; on confirm the tag is removed and `nginx:latest` remains

### Requirement: Image inspect
The images view SHALL open a scrollable inspect view showing the image's full engine inspection data as pretty-printed JSON when the user presses `i`.

#### Scenario: Inspect an image
- **WHEN** the user presses `i` on `nginx:1.27`
- **THEN** the inspect view shows its inspection JSON with breadcrumb `drydock > images > nginx:1.27 > inspect`

### Requirement: Pull image
The images view SHALL prompt for an image reference when the user presses `p`, pull it anonymously from its registry, show in-progress feedback while pulling, and report success or failure.

#### Scenario: Successful pull
- **WHEN** the user presses `p`, enters `redis:7`, and presses Enter
- **THEN** the status area shows the pull is in progress, the UI stays responsive, and on completion `redis:7` appears in the list with a success message

#### Scenario: Reference without tag
- **WHEN** the user enters `redis`
- **THEN** drydock pulls `redis:latest`

#### Scenario: Pull fails
- **WHEN** the user enters a reference that does not exist
- **THEN** the status area shows an error including the registry's message

#### Scenario: Cancel the prompt
- **WHEN** the pull prompt is open and the user presses Esc
- **THEN** no pull is started

### Requirement: Prune dangling images
The images view SHALL, when the user presses `P`, show a confirmation listing the dangling images not used by any container (running or stopped), which are the ones that would be removed, with their total size, and on confirm remove them and report how much space was reclaimed.

#### Scenario: Prune with dangling images
- **WHEN** there are 3 dangling images totaling 450 MB and the user presses `P`
- **THEN** the confirmation lists the 3 images and the 450 MB total; on confirm they are removed and the status area reports the reclaimed space

#### Scenario: Dangling image used by a stopped container is kept
- **WHEN** a dangling image is used by stopped container `old` and the user presses `P`
- **THEN** that image is neither listed in the confirmation nor removed

#### Scenario: Nothing to prune
- **WHEN** there are no unused dangling images and the user presses `P`
- **THEN** no confirmation is shown and the status area states there is nothing to prune

### Requirement: Navigate to containers using an image
The images view SHALL, when the user presses Enter on an image, push a containers view filtered to the containers (running and stopped) created from that image.

#### Scenario: Drill into users of an image
- **WHEN** the user presses Enter on `nginx:1.27`
- **THEN** a containers view listing `web` and `old` is shown with breadcrumb `drydock > images > nginx:1.27 > containers`, and Esc returns to the images view
