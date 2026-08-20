/** Shared TypeScript contracts for /v1/storage routes. */

export type StorageBackendType = 'internal' | 'external' | string;

export interface StorageError {
  error: string;
  event?: string;
  deleted?: number;
}

export interface StorageConfigResponse {
  storage_type: StorageBackendType;
  endpoint: string;
  bucket: string;
  region: string;
}

export interface CreateStorageSessionResponse {
  mode: 'sts' | 'presigned' | string;
  storage_type: StorageBackendType;
  endpoint?: string;
  bucket: string;
  prefix: string;
  region?: string;
  access_key?: string;
  secret_key?: string;
  session_token?: string;
  expires_in: number;
}

export interface StorageQuotaResponse {
  quota_bytes: number;
  used_bytes: number;
  reserved_bytes: number;
  available_bytes: number;
}

export interface PutExternalStorageConfigRequest {
  endpoint: string;
  bucket: string;
  region: string;
  access_key: string;
  secret_key: string;
}

export interface PutExternalStorageConfigResponse {
  storage_type: StorageBackendType;
  sts_supported: boolean;
}

export interface TestExternalStorageConfigRequest {
  endpoint: string;
}

export interface TestExternalStorageConfigResponse {
  ok: boolean;
  cached?: boolean;
  status?: number;
  error?: string;
}

export interface CreatePresignedUploadRequest {
  file_name: string;
  content_type?: string;
  size: number;
  idempotency_key?: string;
}

export interface CreatePresignedUploadResponse {
  key: string;
  url: string;
  expires_in: number;
  reservation_id: string;
  idempotency_key: string;
  reserved_size: number;
}

export interface CommitStorageUploadRequest {
  key: string;
  reservation_id?: string;
}

export interface CommitStorageUploadResponse {
  key: string;
  size: number;
  etag: string;
  modified_at: string;
}

export interface StorageObject {
  key: string;
  size: number;
  etag: string;
  modified_at: string;
  url: string;
}

export interface ListStorageObjectsResponse {
  objects: StorageObject[];
}

export interface StorageObjectQuery {
  limit?: number;
}

export interface DeleteAllStorageObjectsResponse {
  deleted: number;
}

export interface StorageHealthResponse {
  ok: boolean;
  error?: string;
}

export type StorageMigrationState =
  | 'pending'
  | 'running'
  | 'failed'
  | 'completed'
  | 'cancelled'
  | string;

export interface StartStorageMigrationResponse {
  id: number;
  state: StorageMigrationState;
  source_type: StorageBackendType;
  target_type: StorageBackendType;
}

export interface StorageMigration {
  ID: number;
  UserID: number;
  State: StorageMigrationState;
  Copied: number;
  Verified: number;
  Total: number;
  Deleted: number;
  LastError: string;
  VerifiedKeys: string[];
  UpdatedAt: string;
}

export interface StorageMigrationParams {
  id: number | string;
}

export interface PresignStorageDownloadQuery {
  key: string;
  ttl?: number;
}

export interface PresignStorageDownloadResponse {
  key: string;
  url: string;
  expires_in: number;
}

export type StorageRouteContracts = {
  getConfig: {
    response: StorageConfigResponse;
  };
  createSession: {
    response: CreateStorageSessionResponse;
  };
  getQuota: {
    response: StorageQuotaResponse;
  };
  putExternalConfig: {
    request: PutExternalStorageConfigRequest;
    response: PutExternalStorageConfigResponse;
  };
  testExternalConfig: {
    request: TestExternalStorageConfigRequest;
    response: TestExternalStorageConfigResponse;
  };
  switchToInternal: {
    response: void;
  };
  createPresignedUpload: {
    request: CreatePresignedUploadRequest;
    response: CreatePresignedUploadResponse;
  };
  commitUpload: {
    request: CommitStorageUploadRequest;
    response: CommitStorageUploadResponse;
  };
  listObjects: {
    query: StorageObjectQuery;
    response: ListStorageObjectsResponse;
  };
  deleteObject: {
    query: { key: string };
    response: void;
  };
  deleteAllObjects: {
    response: DeleteAllStorageObjectsResponse;
  };
  health: {
    response: StorageHealthResponse;
  };
  startMigration: {
    response: StartStorageMigrationResponse;
  };
  getMigration: {
    params: StorageMigrationParams;
    response: StorageMigration;
  };
  cancelMigration: {
    params: StorageMigrationParams;
    response: void;
  };
  presignDownload: {
    query: PresignStorageDownloadQuery;
    response: PresignStorageDownloadResponse;
  };
};
