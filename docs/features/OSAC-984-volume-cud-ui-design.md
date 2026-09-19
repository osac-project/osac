---
title: volume-cud-console-ui
authors:
  - AI-assisted (design skill)
creation-date: 2026-09-17
tracking-link:
  - https://redhat.atlassian.net/browse/OSAC-984
prd:
  - "enhancements/OSAC-984-create-update-delete-standalone-volumes/prd.md"
see-also:
  - "enhancements/OSAC-984-create-update-delete-standalone-volumes/design.md (backend API design)"
  - "OSAC-4542 (Volume Get/List public API)"
  - "OSAC-4884 (attach/detach — future)"
---

# Volume CUD Console UI

## 1. Overview

Add Create, Update, and Delete volume management to the OSAC console. This
design covers the tenant-facing UI that consumes the public Volume CUD API
(OSAC-2685) at `/api/fulfillment/v1/volumes`. The backend API and its
implementation are complete; this document specifies the console experience.

The UI delivers four views: a volume list page (extending OSAC-4542's
read-only list), a create volume form, a volume details page with metadata
editing, and a delete confirmation modal. All views follow existing OSAC UI
patterns (PatternFly 6, Formik + Yup, TanStack Query, Connect/gRPC-Web)
and reuse shared components from `libs/ui-components`.

## 2. Goals and Non-Goals

### Goals

- Enable Tenant Users and Tenant Admins to create, update, and delete
  standalone storage volumes through the OSAC console.
- Display volume lifecycle states (Creating, Available, Failed, Deleting)
  with clear visual indicators and state-appropriate available actions.
- Surface actionable error messages for all failure modes (invalid input,
  duplicate name, version conflict, unauthorized access, backend failure).
- Maintain parity with the API and CLI — the console must show the same
  states, behaviors, and error results as the other interfaces (explicit
  PRD requirement).
- Follow established OSAC UI conventions so the volume experience is
  consistent with compute instances, clusters, and other managed resources.
- Meet WCAG 2.1 AA accessibility standards.

### Non-Goals

- Cloud Provider Admin cross-tenant volume management UI — Cloud Provider
  Admins use the same tenant-scoped views; no separate admin volume page is
  introduced.
- Volume attach/detach UI — covered by OSAC-4884.
- Volume list view design — covered by OSAC-4542. This design extends that
  list with CUD actions but does not redesign the list itself.
- Volume expansion, snapshot, clone, or backup UI.
- File/object storage or NFS volume creation.

## 3. Motivation / Background

OSAC-4542 delivered read-only volume visibility (Get/List) in the console.
OSAC-2685 added Create, Update, and Delete endpoints to the public API. The
console still has no way for tenants to manage volume lifecycle — they must
use the CLI or API directly. This design closes that gap.

The existing admin-facing storage management UI (storage tiers and backends
at `/admin/infrastructure/storage/`) proves the OSAC component patterns
work for storage resources. The volume CUD UI follows the same patterns but
targets tenant users at a different navigation path.

## 4. Design

### 4.1 Architecture

#### Component Hierarchy

```
osac-ui/
├── apps/app-frontend/src/shell/
│   └── VolumeRoutes.tsx              # Route definitions
├── libs/ui-components/src/
│   ├── api/v1/
│   │   └── volumes.ts                # API hooks (useVolumes, useVolume, useCreateVolume, etc.)
│   ├── components/Volume/
│   │   ├── VolumeCreatePage.tsx       # Create + Edit form (shared component)
│   │   ├── VolumeDeleteConfirmModal.tsx
│   │   ├── VolumeStatusLabel.tsx      # State → ResourceStatusLabel mapping
│   │   ├── VolumeActionsMenu.tsx      # Kebab menu (Edit, Delete)
│   │   └── VolumeDetailsActionButtons.tsx
│   └── pages/tenant/
│       ├── VolumesListPage.tsx        # List view with toolbar + table
│       └── VolumeDetailsPage.tsx      # Details view with metadata sections
```

#### Data Flow

```
Browser
  → Connect JSON (POST/PATCH/DELETE /api/fulfillment/v1/volumes)
  → Go proxy (Connect JSON → native gRPC bridge)
  → fulfillment-service gRPC server
  → public VolumesServer (inMapper, validation, delegation)
  → private VolumesServer → GenericDAO → PostgreSQL
  → Response: public Volume (status fields stripped by outMapper)
  → TanStack Query cache update → React re-render
```

The UI talks exclusively to the **public** API. Private fields
(`vendor_volume_id`, `backend`, `protocol`, `hub`, `vendor_context`) never
reach the browser.

#### Technology Stack

| Layer | Technology | Notes |
|---|---|---|
| UI framework | React 19 + PatternFly 6 | Existing stack |
| Forms | Formik + Yup | Existing pattern (`OsacForm` wrapper, ESLint-enforced) |
| API client | `@connectrpc/connect-web` | Connect JSON transport via `useApiFetch` |
| Server state | TanStack Query 5 | `useApiQuery` for reads, `useMutation` for writes |
| Types | `@osac/types` (generated) | `pnpm gen-types` after public volume proto is available |
| i18n | i18next | English text as key, `useTranslation` from `@osac/ui-components` |

### 4.2 Routing

New routes under the tenant navigation:

| Path | Component | Purpose |
|---|---|---|
| `/storage/volumes` | `VolumesListPage` | Volume list with table, toolbar, pagination |
| `/storage/volumes/create` | `VolumeCreatePage` | Create volume form |
| `/storage/volumes/:id` | `VolumeDetailsPage` | Volume details with metadata sections |
| `/storage/volumes/:id/edit` | `VolumeCreatePage` | Edit mutable metadata (same component as create) |

Route file (`VolumeRoutes.tsx`):

```tsx
export const VolumeRoutes = () => (
  <Routes>
    <Route index element={<VolumesListPage />} />
    <Route path="create" element={<VolumeCreatePage />} />
    <Route path=":id" element={<VolumeDetailsPage />} />
    <Route path=":id/edit" element={<VolumeCreatePage />} />
  </Routes>
);
```

The sidebar navigation adds a "Volumes" entry under a "Storage" section in
the tenant navigation, following the pattern used by Compute and Networking.

### 4.3 Volume List Page

The volume list page extends the OSAC-4542 read-only list with CUD
capabilities.

#### Toolbar

| Element | Behavior |
|---|---|
| **"Create volume" button** | Primary button, navigates to `/storage/volumes/create` |
| **Filter/search** | CEL-based filter on name, state, storage_tier (existing pattern) |
| **Pagination** | Standard offset/limit pagination (existing `ListParams` pattern) |

#### Table Columns

| Column | Source Field | Sortable | Notes |
|---|---|---|---|
| Name | `metadata.name` | Yes | Link to details page. Shows `metadata.display_name` as subtitle when set. |
| Status | `status.state` | Yes | `VolumeStatusLabel` component |
| Storage Tier | `spec.storage_tier` | Yes | Tier name as text |
| Size | `spec.size_gib` | Yes | Formatted as "{n} GiB" |
| Access Mode | `spec.access_mode` | No | Human-readable label (see mapping below) |
| Created | `metadata.creation_timestamp` | Yes | Relative time ("3 hours ago") with tooltip showing absolute time |
| Actions | — | No | Kebab menu (`VolumeActionsMenu`) |

#### Access Mode Display Labels

| Enum Value | Display Label |
|---|---|
| `VOLUME_ACCESS_MODE_READ_WRITE_ONCE` | ReadWriteOnce |
| `VOLUME_ACCESS_MODE_READ_ONLY_MANY` | ReadOnlyMany |
| `VOLUME_ACCESS_MODE_READ_WRITE_MANY` | ReadWriteMany |
| `VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD` | ReadWriteOncePod |

These labels match the Kubernetes PersistentVolume access mode names that
cloud-native users expect.

#### Row Actions (Kebab Menu)

| Action | Available States | Disabled States | Behavior |
|---|---|---|---|
| **Edit** | CREATING, AVAILABLE, FAILED | DELETING, DELETED | Navigate to `/storage/volumes/:id/edit` |
| **Delete** | AVAILABLE, FAILED | CREATING, DELETING, DELETED | Open `VolumeDeleteConfirmModal` |

Actions in disabled states are hidden from the kebab menu, not shown as
greyed-out items — this follows the OSAC convention of only displaying
actions that are currently valid.

#### Empty State

When no volumes exist, display a centered empty state:

- **Icon:** Storage/database icon
- **Heading:** "No volumes"
- **Description:** "Create a volume to prepare storage independently from
  compute resources."
- **Action:** Primary "Create volume" button

#### Polling for State Transitions

Volumes in `CREATING` or `DELETING` state are transient. The list page uses
TanStack Query's `refetchInterval` to poll for updates:

- **When any visible volume is in CREATING or DELETING:** Poll every 5
  seconds.
- **When all visible volumes are in terminal states (AVAILABLE, FAILED,
  DELETED):** Stop polling (default `staleTime`).

This matches the existing compute instance list polling pattern.

### 4.4 Create Volume Page

A full-page form following the `StorageTierCreatePage` pattern.

#### Page Layout

```
┌──────────────────────────────────────────────────────────┐
│ Breadcrumb: Storage > Volumes > Create                   │
│                                                          │
│ Title: Create volume                                     │
│                                                          │
│ ┌──────────────────────────────────────────────────────┐ │
│ │ OsacForm                                             │ │
│ │                                                      │ │
│ │ Name *                                               │ │
│ │ ┌──────────────────────────────────────────────────┐ │ │
│ │ │ my-volume-name                                   │ │ │
│ │ └──────────────────────────────────────────────────┘ │ │
│ │ Helper: Lowercase letters, digits, and hyphens.      │ │
│ │         Max 63 characters. Cannot be changed later.  │ │
│ │                                                      │ │
│ │ Storage tier *                                       │ │
│ │ ┌──────────────────────────────────────────────────┐ │ │
│ │ │ Select a storage tier                        ▼   │ │ │
│ │ └──────────────────────────────────────────────────┘ │ │
│ │                                                      │ │
│ │ Size (GiB) *                                         │ │
│ │ ┌──────────────────────────────────────────────────┐ │ │
│ │ │ 100                                              │ │ │
│ │ └──────────────────────────────────────────────────┘ │ │
│ │ Helper: Must be greater than zero.                   │ │
│ │         Cannot be changed after creation.            │ │
│ │                                                      │ │
│ │ Access mode *                                        │ │
│ │ ○ ReadWriteOnce     ○ ReadOnlyMany                   │ │
│ │ ○ ReadWriteMany     ○ ReadWriteOncePod               │ │
│ │                                                      │ │
│ │ Display name                                         │ │
│ │ ┌──────────────────────────────────────────────────┐ │ │
│ │ │                                                  │ │ │
│ │ └──────────────────────────────────────────────────┘ │ │
│ │                                                      │ │
│ │ Description                                          │ │
│ │ ┌──────────────────────────────────────────────────┐ │ │
│ │ │                                                  │ │ │
│ │ └──────────────────────────────────────────────────┘ │ │
│ └──────────────────────────────────────────────────────┘ │
│                                                          │
│ [!] Inline danger alert (shown on API error)             │
│                                                          │
│ [ Create ]  Cancel                                       │
└──────────────────────────────────────────────────────────┘
```

#### Form Fields

| Field | Component | Yup Schema | Required | Notes |
|---|---|---|---|---|
| Name | `NameField` | `resourceNameSchema(t)` | Yes | DNS label: `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`. Disabled in edit mode. |
| Storage tier | `StorageTierSelectField` | `Yup.string().required()` | Yes | Fetches active tiers via public API. Disabled in edit mode. |
| Size (GiB) | `InputField` (type="number") | `positiveIntegerSchema(t)` | Yes | Must be > 0. Disabled in edit mode. |
| Access mode | `RadioButtonField` | `Yup.string().oneOf([...]).required()` | Yes | 4 options. Disabled in edit mode. |
| Display name | `InputField` | `Yup.string().max(63)` | No | Optional human-readable label. Max 63 chars. |
| Description | `InputField` (textarea) | `Yup.string()` | No | Free-text description. |

#### Create vs. Edit Mode

A single `VolumeCreatePage` component handles both modes, toggled by the
presence of the `:id` route parameter (matching the `StorageTierCreatePage`
pattern):

**Create mode** (`/storage/volumes/create`):
- All fields editable
- Title: "Create volume"
- Submit button: "Create"
- On success: navigate to `/storage/volumes/:id` (details page)

**Edit mode** (`/storage/volumes/:id/edit`):
- Immutable fields (name, storage_tier, size_gib, access_mode) rendered as
  disabled inputs with helper text: "Cannot be changed after creation"
- Disabled fields use `aria-describedby` pointing to the helper text for
  screen reader accessibility
- Mutable fields (display_name, description) remain editable
- Title: "Edit volume"
- Submit button: "Save"
- On success: navigate back to `/storage/volumes/:id`

Labels and annotations are managed separately (see section 4.5 Details
Page) because they use a key-value editor pattern that does not fit a
simple form field.

#### Validation

Client-side validation (Formik + Yup) fires on blur and on submit:

```typescript
const getVolumeSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({ name: resourceNameSchema(t) }),
    displayName: Yup.string().max(63, t('Display name must be at most 63 characters')),
    description: Yup.string(),
    storageTier: Yup.string().required(t('Storage tier is required')),
    sizeGib: positiveIntegerSchema(t).required(t('Size is required')),
    accessMode: Yup.string()
      .oneOf(
        ['READ_WRITE_ONCE', 'READ_ONLY_MANY', 'READ_WRITE_MANY', 'READ_WRITE_ONCE_POD'],
        t('Access mode is required'),
      )
      .required(t('Access mode is required')),
  });
```

Server-side errors (API responses) are caught and displayed as an inline
danger alert below the form, using `getErrorMessage(error)` to extract
human-readable text from the gRPC error.

#### Form Behavior

- **`LeaveFormConfirmation`** — included to prompt the user when navigating
  away with unsaved changes.
- **Submit loading state** — the Create/Save button shows a spinner
  (`isLoading={isSubmitting}`) and is disabled during submission.
- **Cancel** — link-styled button, navigates back to the list or details
  page.

### 4.5 Volume Details Page

Displays a single volume's full information with action buttons and
editable metadata sections.

#### Page Layout

```
┌──────────────────────────────────────────────────────────┐
│ Breadcrumb: Storage > Volumes > {name}                   │
│                                                          │
│ ┌──────────────────────────────────────────────────────┐ │
│ │ ResourceDetailHeader                                 │ │
│ │ Title: {metadata.name}          [Edit] [Delete]      │ │
│ │ Status: VolumeStatusLabel                            │ │
│ │ Subtitle: {metadata.display_name} (if set)           │ │
│ └──────────────────────────────────────────────────────┘ │
│                                                          │
│ ┌─ Spec ─────────────────────────────────────────────┐   │
│ │ Storage Tier    standard-block                      │   │
│ │ Size            100 GiB                             │   │
│ │ Access Mode     ReadWriteOnce                       │   │
│ └─────────────────────────────────────────────────────┘   │
│                                                          │
│ ┌─ Status ───────────────────────────────────────────┐   │
│ │ State           Available  (VolumeStatusLabel)      │   │
│ │ Message         (shown only when set, e.g. errors)  │   │
│ └─────────────────────────────────────────────────────┘   │
│                                                          │
│ ┌─ Metadata ─────────────────────────────────────────┐   │
│ │ Display Name    My analytics volume    [pencil]     │   │
│ │ Description     Primary data store     [pencil]     │   │
│ │ Created         2026-09-15 14:32:10 UTC             │   │
│ │ ID              abc-123-def-456                      │   │
│ └─────────────────────────────────────────────────────┘   │
│                                                          │
│ ┌─ Labels ───────────────────────────────────────────┐   │
│ │ env=production  team=analytics         [Edit]       │   │
│ └─────────────────────────────────────────────────────┘   │
│                                                          │
│ ┌─ Annotations ──────────────────────────────────────┐   │
│ │ cost-center=CC-4421                    [Edit]       │   │
│ └─────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
```

#### Header Action Buttons

| Button | Variant | Visible When | Behavior |
|---|---|---|---|
| **Edit** | Secondary | CREATING, AVAILABLE, FAILED | Navigate to `/storage/volumes/:id/edit` |
| **Delete** | Secondary (not danger) | AVAILABLE, FAILED | Open `VolumeDeleteConfirmModal` |

Buttons are hidden (not disabled) in states where the action is invalid,
following PatternFly's convention that delete triggers should not use danger
styling.

#### Failed State Message

When a volume is in FAILED state, the `status.message` field (set by the
backend reconciler) is displayed in an inline danger alert at the top of
the details page, below the header:

```
Warning: Volume provisioning failed
  Backend reported: <status.message content>
```

This gives the user an actionable error message. The volume can still have
its metadata updated or be deleted from this state.

#### Inline Metadata Editing

The details page supports inline editing for `display_name` and
`description` using PatternFly's field-specific inline edit pattern:

- A pencil icon appears beside each editable field.
- Clicking the icon switches that field to edit mode (text input appears).
- Check icon saves; close icon cancels.
- The save action calls the Update API with `update_mask` targeting only
  the changed field and `lock=true` for optimistic locking.
- On success, TanStack Query cache is invalidated to refresh the display.

Labels and annotations are edited via a separate modal (matching the
existing OSAC pattern for key-value pairs). The "Edit" link on each section
opens a modal with a key-value editor allowing add, modify, and remove
operations.

#### State-Dependent Edit Availability

Inline edit icons and the Edit button are **hidden** when the volume is in
`DELETING` or `DELETED` state. The details page still displays the volume
information in read-only mode during these states.

When the volume is in `CREATING` state, metadata editing remains available
(the PRD explicitly allows metadata updates during creation).

#### Polling on Details Page

When the volume is in `CREATING` or `DELETING` state, the details page
polls for updates every 5 seconds. The `VolumeStatusLabel` updates
automatically when the volume transitions to its next state.

### 4.6 Volume Status Label

A new `VolumeStatusLabel` component maps `VolumeState` enum values to the
existing `ResourceStatusLabel`:

```typescript
const VOLUME_STATUS_MAP: Record<VolumeState, { status: StatusKind; text: string }> = {
  [VolumeState.UNSPECIFIED]:  { status: 'unspecified',  text: 'Unknown'  },
  [VolumeState.CREATING]:     { status: 'progressing',  text: 'Creating' },
  [VolumeState.AVAILABLE]:    { status: 'ready',        text: 'Available'},
  [VolumeState.FAILED]:       { status: 'failed',       text: 'Failed'   },
  [VolumeState.DELETING]:     { status: 'progressing',  text: 'Deleting' },
  [VolumeState.DELETED]:      { status: 'unspecified',  text: 'Deleted'  },
};
```

| State | Color | Icon |
|---|---|---|
| Creating | Blue | InProgressIcon |
| Available | Green | CheckCircleIcon |
| Failed | Red | ExclamationCircleIcon |
| Deleting | Blue | InProgressIcon |
| Deleted | Grey | QuestionCircleIcon |
| Unknown | Grey | QuestionCircleIcon |

### 4.7 Delete Confirmation

Uses the existing `DeleteResourceModal`:

```tsx
const VolumeDeleteConfirmModal = ({ volume, onClose, onSuccess }) => {
  const { t } = useTranslation();
  const deleteVolume = useDeleteVolume();
  const volumeName = volume.metadata?.name ?? volume.id;

  return (
    <DeleteResourceModal
      resourceName={volumeName}
      label={t(
        'This permanently deletes the volume and all of its data. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete volume')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteVolume}
      variables={volume.id}
    />
  );
};
```

**Delete behavior by state:**

| Current State | Delete Action | Result |
|---|---|---|
| AVAILABLE | Delete button visible | Modal opens; API sets state to DELETING |
| FAILED | Delete button visible | Modal opens; API sets state to DELETING |
| CREATING | Delete button hidden | User must wait for creation to complete or fail |
| DELETING | Delete button hidden | Deletion already in progress |
| DELETED | Not visible (archived) | Volume not shown in list |

**Post-delete navigation:** On successful delete, navigate back to the
volume list. An inline success alert ("Volume deleted") is shown briefly on
the list page.

**Idempotent delete:** If the user manages to trigger delete for a volume
that is already in DELETING state (race condition), the API returns success
(idempotent). No error is shown.

### 4.8 API Hooks

New hooks in `libs/ui-components/src/api/v1/volumes.ts`:

```typescript
// Read hooks
export const useVolumes = (params: ListParams = {}) => {
  const client = useApiFetch(Volumes);
  return useApiQuery({
    queryKey: apiQueryKey('v1/volumes', undefined, params),
    queryFn: () => client.list(params),
    select: (data) => data.items,
  });
};

export const useVolume = (id: string) => {
  const client = useApiFetch(Volumes);
  return useApiQuery({
    queryKey: apiQueryKey('v1/volumes', [id]),
    queryFn: () => client.get({ id }),
    select: (data) => data.object,
    enabled: Boolean(id),
  });
};

// Mutation hooks
export const useCreateVolume = () => {
  const client = useApiFetch(Volumes);
  const queryClient = useApiQueryClient();
  return useMutation({
    mutationFn: (volume: PartialMessage<Volume>) =>
      client.create({ object: volume }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: apiQueryKey('v1/volumes') });
    },
  });
};

export const useUpdateVolume = () => {
  const client = useApiFetch(Volumes);
  const queryClient = useApiQueryClient();
  return useMutation({
    mutationFn: ({
      volume,
      updateMask,
    }: {
      volume: PartialMessage<Volume>;
      updateMask?: string[];
    }) =>
      client.update({ object: volume, updateMask, lock: true }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: apiQueryKey('v1/volumes', [variables.volume.id!]),
      });
      queryClient.invalidateQueries({ queryKey: apiQueryKey('v1/volumes') });
    },
  });
};

export const useDeleteVolume = () => {
  const client = useApiFetch(Volumes);
  const queryClient = useApiQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.delete({ id }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: apiQueryKey('v1/volumes') });
    },
  });
};
```

**Prerequisites:** The `Volumes` service descriptor must be exported from
`@osac/types`. This requires running `pnpm gen-types` after the public
volume proto files are included in the UI's protobuf source. Additionally,
`'v1/volumes'` must be registered as an `ApiRoute` in
`libs/ui-components/src/api/types.ts`.

### 4.9 Error Handling

#### gRPC Error Code to UI Message Mapping

| gRPC Code | Backend Message (example) | Alert Title | Alert Variant |
|---|---|---|---|
| `InvalidArgument` | "field 'metadata.name' is required" | "Failed to create volume" / "Failed to update volume" | danger |
| `AlreadyExists` | "volume with name 'x' already exists in tenant 'y'" | "Failed to create volume" | danger |
| `NotFound` | standard not-found | "Volume not found" | danger |
| `FailedPrecondition` | "volume in state 'DELETING' cannot be updated" | "Failed to update volume" | danger |
| `Aborted` | "optimistic lock failure: version mismatch" | "This volume was modified" (see below) | warning |
| `Unauthenticated` | — | Redirects to login (handled by `connectErrorInterceptor`) | — |
| `PermissionDenied` | — | "You do not have permission to perform this action" | danger |
| `Internal` | "failed to process volume" | "An unexpected error occurred" | danger |

#### Optimistic Locking Conflict (Aborted)

When the update API returns `Aborted` (version mismatch), the UI shows a
distinct warning alert (not danger) with a refresh action:

```
Warning: This volume was modified
  Another user or process updated this volume while you were editing.
  [ Refresh and retry ]
```

The "Refresh and retry" action:
1. Invalidates the TanStack Query cache for the volume.
2. Re-fetches the volume from the API.
3. Resets the form with the fresh values.
4. The user can then re-apply their changes and submit again.

### 4.10 Security Considerations

- **No private fields reach the browser.** The public API and its outMapper
  strip `vendor_volume_id`, `backend`, `protocol`, `hub`, and
  `vendor_context` before the response leaves the fulfillment service.
- **Status field is read-only.** The inMapper on the server ignores
  client-set status fields. The create form does not include status inputs.
- **Tenant isolation is server-enforced.** The UI does not implement
  tenant-scoping logic — the API handles it via gRPC interceptors.
- **Input validation is defense-in-depth.** Client-side Yup validation
  catches common errors before the API call. Server-side validation is
  authoritative.

### 4.11 Failure Handling and Recovery

| Scenario | UI Behavior |
|---|---|
| **Create fails (invalid input)** | Inline danger alert with backend error message. Form remains populated — user corrects and retries. |
| **Create fails (duplicate name)** | Inline danger alert: "volume with name 'x' already exists...". User chooses a different name. |
| **Create fails (NFS tier selected)** | Inline danger alert with protocol-specific message from backend. User selects a block-protocol tier. |
| **Create fails (network/server error)** | Inline danger alert: "An unexpected error occurred". User retries. |
| **Create succeeds, volume stays CREATING** | Details page polls every 5s. Status label shows blue "Creating". |
| **Create succeeds, volume moves to FAILED** | Details page shows red "Failed" status with `status.message` in danger alert. Edit and Delete remain available. |
| **Update fails (version conflict)** | Warning alert: "This volume was modified". User clicks "Refresh and retry". |
| **Update fails (immutable field)** | Should not occur (fields are disabled in UI). If it does, inline danger alert shows server message. |
| **Update fails (volume is DELETING)** | Should not occur (edit is hidden in DELETING state). If it does, inline danger alert. |
| **Delete fails (not found)** | Modal shows inline danger alert: "Volume not found". User closes modal; list refreshes. |
| **Delete fails (server error)** | Modal shows inline danger alert: "Failed to delete volume" with error detail. User can retry or close. |
| **API unreachable** | TanStack Query shows loading state, then error after timeout. `QueryErrorState` component renders. |

### 4.12 RBAC / Tenancy

The UI does not implement authorization checks. All authorization is
enforced by the API's OPA layer. The UI renders actions (Create, Edit,
Delete) for all authenticated users and handles `PermissionDenied`
responses by displaying an appropriate error message.

### 4.13 Extensibility / Future-Proofing

| Future Capability | UI Impact |
|---|---|
| **NFS/file-storage support (OSAC-4515)** | No UI change needed — the `StorageTierSelectField` already shows all active tiers. |
| **Attach/detach (OSAC-4884)** | Details page adds an "Attachments" section. Delete button may show a warning when attachments exist. |
| **Volume expansion** | Details page adds a "Resize" action. Size field becomes editable in a resize-specific flow. |
| **Snapshots/clones** | Details page adds a "Snapshots" tab. |
| **Labels/annotations editor** | The key-value editor modal is designed as a reusable component. |
| **Bulk delete** | The list page's checkbox selection pattern supports future bulk actions. |

## 5. Accessibility

### WCAG 2.1 AA Compliance

| Concern | Implementation |
|---|---|
| **Required fields (SC 1.3.1)** | All required fields use `required` attribute AND visible asterisk (*) with legend |
| **Error identification (SC 3.3.1)** | Formik validation messages appear as helper text below the field, not color-alone. `aria-describedby` links field to error. |
| **Error suggestion (SC 3.3.3)** | Yup messages are actionable: "Name must only contain lowercase letters, digits, and hyphens" not "Invalid name" |
| **Status messages (SC 4.1.3)** | Volume state transitions announced via `role="status"` live region. API errors use PatternFly `Alert` (implicit `role="alert"`). |
| **Focus management (SC 2.4.3)** | After form submission with errors, focus moves to first error field. After modal close, focus returns to trigger element. |
| **Disabled field explanation (SC 4.1.2)** | Immutable fields in edit mode use `aria-describedby` pointing to helper text: "Cannot be changed after creation" |
| **Delete confirmation (SC 3.2.2)** | PatternFly `Modal` provides `role="dialog"`, `aria-modal="true"`, focus trapping, and Escape key dismissal. |
| **Keyboard navigation** | All actions reachable via keyboard. Tab order follows visual order. Kebab menu opens with Enter/Space. |

### Screen Reader Announcements

- **Volume created:** Page title change is announced by the router.
- **Volume deleted:** Inline success alert announced via `role="alert"`.
- **State transition:** Status label update announced via `role="status"` live region.

## 6. Alternatives Considered

| Alternative | Why Rejected |
|---|---|
| **Modal-based create form** | Create form has 6+ fields including a remote-data dropdown. PatternFly recommends modals only for simple confirmations. Full-page form is the OSAC convention. |
| **Inline table editing for metadata** | Cumbersome for multi-field metadata. Details page inline edit provides a better experience. |
| **Separate edit page for labels/annotations** | Key-value pairs are better served by a modal editor. Avoids unnecessary page navigation. |
| **Type-to-confirm on delete** | Existing `DeleteResourceModal` does not include it. Adding for volumes only would be inconsistent. |

## 7. Impact and Compatibility

### New Dependencies

- **`@osac/types` update:** Public volume types must be generated (`pnpm gen-types`).
- **`ApiRoute` registration:** `'v1/volumes'` must be added to the API route type union.

### No Breaking Changes

- Existing admin storage management UI unaffected.
- No changes to the Go proxy, backend API, or shared components.

### Navigation Changes

- "Volumes" link added to tenant sidebar under a "Storage" section.

## 8. Open Questions

| # | Question | Owner | Impact |
|---|---|---|---|
| 1 | Should the volume list page live under `/storage/volumes` (new Storage nav section) or extend the existing admin storage routes? | UX/Product | Determines navigation structure. Recommendation: new tenant-facing routes. |
| 2 | Should labels and annotations be editable from the create form, or only from the details page after creation? | UX | Recommendation: details page only, matching the simpler create form pattern. |
| 3 | Should the UI show CSI-provisioned volumes differently from user-created volumes? | Product | CSI volumes have auto-generated names (`pvc-{UID}`). Recommendation: defer to OSAC-4542 list design. |

## 9. Test Plan

### Requirement Traceability

| PRD Requirement | Test Cases | Type |
|---|---|---|
| Create volume through console | TC-UI-C1, TC-UI-C2, TC-UI-C3 | Unit, Playwright |
| Update volume metadata through console | TC-UI-U1, TC-UI-U2, TC-UI-U3 | Unit |
| Delete volume through console | TC-UI-D1, TC-UI-D2 | Unit |
| Show lifecycle status | TC-UI-S1, TC-UI-S2 | Unit |
| Actionable error display | TC-UI-E1, TC-UI-E2, TC-UI-E3, TC-UI-E4 | Unit |
| Same states/behavior as API and CLI | TC-UI-S1, TC-UI-S2, TC-UI-E1-E4 | Unit |
| Tenant-scoped permissions | TC-UI-A1 | Unit |

### Unit Tests (Vitest + React Testing Library)

**VolumeCreatePage:**
- TC-UI-C1: Renders all required fields; submitting empty shows validation errors.
- TC-UI-C2: Successful creation calls the API with correct payload and navigates to details.
- TC-UI-C3: API error displays inline danger alert with server message.

**VolumeCreatePage (edit mode):**
- TC-UI-U1: Immutable fields are disabled.
- TC-UI-U2: Metadata update calls API with `lock=true` and correct `update_mask`.
- TC-UI-U3: Version conflict shows warning alert with "Refresh and retry".

**VolumeDeleteConfirmModal:**
- TC-UI-D1: Renders modal; Delete calls API and closes on success.
- TC-UI-D2: API error shows inline danger alert within modal.

**VolumeStatusLabel:**
- TC-UI-S1: Each VolumeState renders correct color and text.
- TC-UI-S2: Unknown/undefined state renders grey "Unknown".

**Error display:**
- TC-UI-E1: InvalidArgument shows "Failed to create volume".
- TC-UI-E2: AlreadyExists shows duplicate name message.
- TC-UI-E3: FailedPrecondition shows state-specific message.
- TC-UI-E4: Aborted shows warning variant with refresh action.

**VolumeActionsMenu:**
- TC-UI-A1: Actions hidden based on volume state.

### Manual Verification (Playwright)

Using `apps/playwright/scratch/` against a live cluster:
- Create volume; verify "Creating" transitions to "Available".
- Edit display name; verify change persists.
- Delete volume; verify it disappears from list.
- Duplicate name; verify error message.

## 10. Task Decomposition

### Epic Summary

| # | Epic | Size | Stories | Depends On |
|---|---|---|---|---|
| 1 | Volume API Layer & Shared Components | M | 3 | — |
| 2 | Create Volume | M | 3 | Epic 1 |
| 3 | Volume Details & Metadata Editing | M | 3 | Epics 1, 2 |
| 4 | Volume Deletion | S | 2 | Epics 1, 3 |

**Total: 4 epics, 11 stories (8 [DEV], 1 [QE], 1 [DOCS])**

### Epic 1: Volume API Layer & Shared Components (M)

Foundation: typed API hooks, VolumeStatusLabel, VolumeActionsMenu, list
page, routing, and navigation.

| # | Title | Prefix | Size |
|---|---|---|---|
| 1 | Register public Volume types and API hooks | [DEV] | M |
| 2 | Add VolumeStatusLabel, VolumeActionsMenu, and list page | [DEV] | L |
| 3 | Add volume routing and sidebar navigation | [DEV] | S |

### Epic 2: Create Volume (M)

Full-page form with Formik + Yup, error handling, and e2e test coverage.

| # | Title | Prefix | Size |
|---|---|---|---|
| 1 | Implement VolumeCreatePage with Formik + Yup validation | [DEV] | L |
| 2 | Add create volume error handling and edge cases | [DEV] | M |
| 3 | Volume create, edit, and delete e2e scenarios | [QE] | M |

### Epic 3: Volume Details & Metadata Editing (M)

Details page, edit mode with optimistic locking, inline editing, and
labels/annotations modal.

| # | Title | Prefix | Size |
|---|---|---|---|
| 1 | Implement VolumeDetailsPage with spec, status, and metadata | [DEV] | L |
| 2 | Add edit mode and optimistic locking conflict handling | [DEV] | M |
| 3 | Add inline metadata editing and labels/annotations modal | [DEV] | M |

### Epic 4: Volume Deletion (S)

Delete confirmation modal and user documentation.

| # | Title | Prefix | Size |
|---|---|---|---|
| 1 | Implement VolumeDeleteConfirmModal and deletion flow | [DEV] | S |
| 2 | Document volume management console workflows | [DOCS] | S |

### PRD Requirement Coverage

All 20 PRD requirements are covered by at least one implementing story and
one validating test case. No gaps identified. See the full coverage matrix
in the design artifacts for detailed requirement-to-story-to-test mapping.
