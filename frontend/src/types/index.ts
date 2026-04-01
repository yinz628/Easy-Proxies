// ---- Node & Snapshot types (maps to monitor.Snapshot) ----

export interface NodeInfo {
  tag: string
  name: string
  uri: string
  mode: string
  listen_address?: string
  port?: number
  region?: string
  country?: string
}

export interface TimelineEvent {
  time: string
  success: boolean
  latency_ms: number
  error?: string
  destination?: string
}

export interface NodeSnapshot extends NodeInfo {
  failure_count: number
  success_count: number
  blacklisted: boolean
  blacklisted_until: string
  active_connections: number
  last_error?: string
  last_failure?: string
  last_success?: string
  last_probe_latency?: number
  last_latency_ms: number
  available: boolean
  initial_check_done: boolean
  total_upload: number
  total_download: number
  timeline?: TimelineEvent[]
}

// ---- API Response types ----

export interface NodesResponse {
  nodes: NodeSnapshot[]
  total_nodes: number
  total_upload: number
  total_download: number
  upload_speed?: number
  download_speed?: number
  traffic_sampled?: string
  region_stats: Record<string, number>
  region_healthy: Record<string, number>
}

export interface DebugNode {
  tag: string
  name: string
  mode: string
  port: number
  failure_count: number
  success_count: number
  active_connections: number
  last_latency_ms: number
  last_success: string
  last_failure: string
  last_error: string
  blacklisted: boolean
  total_upload: number
  total_download: number
  timeline: TimelineEvent[]
}

export interface DebugResponse {
  nodes: DebugNode[]
  total_calls: number
  total_success: number
  success_rate: number
}

export interface SettingsData {
  // Global
  mode: string
  log_level: string
  external_ip: string
  skip_cert_verify: boolean

  // Listener
  listener_address: string
  listener_port: number
  listener_protocol: string
  listener_username: string
  listener_password: string

  // Multi-port
  multi_port_address: string
  multi_port_base_port: number
  multi_port_protocol: string
  multi_port_username: string
  multi_port_password: string

  // Pool
  pool_mode: string
  pool_failure_threshold: number
  pool_blacklist_duration: string

  // Management
  management_enabled: boolean
  management_listen: string
  management_probe_target: string
  management_password: string
  management_health_check_interval: string

  // Subscription refresh
  sub_refresh_enabled: boolean
  sub_refresh_interval: string
  sub_refresh_timeout: string
  sub_refresh_health_check_timeout: string
  sub_refresh_drain_timeout: string
  sub_refresh_min_available_nodes: number

  // GeoIP
  geoip_enabled: boolean
  geoip_database_path: string
  geoip_auto_update_enabled: boolean
  geoip_auto_update_interval: string

  // Subscriptions
  subscriptions: string[]
  txt_subscriptions: TXTSubscriptionConfig[]
}

export interface SettingsUpdateResponse {
  message: string
  need_reload: boolean
}

// ---- Auth types ----

export interface AuthResponse {
  message: string
  token?: string
  no_password?: boolean
}

export interface ErrorResponse {
  error: string
}

// ---- Config Node CRUD types ----

export interface ConfigNodePayload {
  name: string
  uri: string
  port: number
  username: string
  password: string
}

export interface ConfigNodeConfig {
  name: string
  uri: string
  port: number
  username: string
  password: string
  source?: string
  disabled?: boolean
  quality_status?: string
  quality_score?: number
  quality_grade?: string
  quality_summary?: string
  quality_checked?: number
  exit_ip?: string
  exit_country?: string
  exit_country_code?: string
  exit_region?: string
}

export interface ConfigNodesResponse {
  nodes: ConfigNodeConfig[]
}

export type ManageStatus = '' | 'normal' | 'unavailable' | 'blacklisted' | 'pending' | 'disabled'
export type ManageSortKey = 'name' | 'status' | 'latency' | 'region' | 'port' | 'source'
export type ManageSortDir = 'asc' | 'desc'

export interface ManageQuery {
  page: number
  page_size: number
  keyword: string
  status: ManageStatus
  region: string
  source: string
  sort_key: ManageSortKey
  sort_dir: ManageSortDir
}

export interface ManageFilterSnapshot {
  keyword: string
  status: ManageStatus
  region: string
  source: string
}

export interface ManageNodeRow extends ConfigNodeConfig {
  runtime_status: Exclude<ManageStatus, ''>
  latency_ms: number
  region?: string
  country?: string
  active_connections: number
  success_count: number
  failure_count: number
  tag?: string
}

export interface ManageListResponse {
  items: ManageNodeRow[]
  page: number
  page_size: number
  total: number
  filtered_total: number
  summary: Record<Exclude<ManageStatus, ''>, number>
  facets: {
    regions: string[]
    sources: string[]
  }
}

export type SelectionState =
  | { mode: 'names'; names: Set<string> }
  | { mode: 'filter'; filter: ManageFilterSnapshot; excludeNames: Set<string> }

export type ManageSelectionRequest =
  | { selection: { mode: 'names'; names: string[] } }
  | { selection: { mode: 'filter'; filter: ManageFilterSnapshot; exclude_names: string[] } }

export interface ConfigNodeMutationResponse {
  node?: ConfigNodeConfig
  message: string
}

export interface TXTSubscriptionConfig {
  name: string
  url: string
  default_protocol: 'http' | 'https' | 'socks5'
  auto_update_enabled: boolean
}

export interface SubscriptionFeedStatus {
  feed_key: string
  name: string
  type: 'legacy' | 'txt'
  url: string
  auto_update_enabled: boolean
  last_refresh?: string
  last_error?: string
  valid_nodes?: number
  skipped_lines?: number
}

// ---- Subscription types ----

export interface SubscriptionStatus {
  enabled: boolean
  has_subscriptions?: boolean
  last_refresh?: string
  next_refresh?: string
  node_count?: number
  last_error?: string
  refresh_count?: number
  is_refreshing?: boolean
  message?: string
  feeds?: SubscriptionFeedStatus[]
}

export interface NodeQualityCheckItem {
  target: string
  status: string
  http_status?: number
  latency_ms?: number
  message?: string
}

export interface NodeQualityCheckResult {
  node_id: number
  quality_status: string
  quality_score?: number
  quality_grade: string
  quality_summary: string
  quality_checked_at?: string
  exit_ip?: string
  exit_country?: string
  exit_country_code?: string
  exit_region?: string
  items: NodeQualityCheckItem[]
}

export interface QualityCheckBatchStart {
  type: 'start'
  total: number
}

export interface QualityCheckBatchProgress {
  type: 'progress'
  tag: string
  name: string
  status: 'success' | 'error'
  error: string
  quality_status?: string
  quality_score?: number
  quality_grade?: string
  quality_summary?: string
  quality_checked_at?: string
  exit_ip?: string
  exit_country?: string
  exit_country_code?: string
  exit_region?: string
  items?: NodeQualityCheckItem[]
  current: number
  total: number
}

export interface QualityCheckBatchComplete {
  type: 'complete'
  total: number
  success: number
  failed: number
}

export type QualityCheckBatchEvent = QualityCheckBatchStart | QualityCheckBatchProgress | QualityCheckBatchComplete

export interface BatchProbeJobResult {
  tag: string
  name: string
  latency_ms: number
  error?: string
}

export interface BatchProbeJob {
  id: string
  status: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'
  started_at: string
  updated_at: string
  completed_at?: string
  total: number
  completed: number
  success: number
  failed: number
  active_workers: number
  requested_tags: string[]
  last_result?: BatchProbeJobResult
  last_error?: string
}

// ---- SSE Probe types ----

export interface ProbeSSEStart {
  type: 'start'
  total: number
}

export interface ProbeSSEProgress {
  type: 'progress'
  tag: string
  name: string
  latency: number
  status: 'success' | 'error'
  error: string
  current: number
  total: number
  progress: number
}

export interface ProbeSSEComplete {
  type: 'complete'
  total: number
  success: number
  failed: number
}

export type ProbeSSEEvent = ProbeSSEStart | ProbeSSEProgress | ProbeSSEComplete

// ---- SSE Traffic stream types ----

export interface TrafficStreamNode {
  tag: string
  upload_speed: number
  download_speed: number
  total_upload: number
  total_download: number
}

export interface TrafficStreamEvent {
  type: 'traffic'
  node_count: number
  total_upload: number
  total_download: number
  upload_speed: number
  download_speed: number
  sampled_at: string
  nodes: TrafficStreamNode[]
}
