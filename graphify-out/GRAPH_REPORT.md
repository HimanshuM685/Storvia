# Graph Report - Storvia  (2026-10-18)

## Corpus Check
- 178 files · ~203,035 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 10 file(s) not represented in the graph (top: (none) 7, .example 1, .css 1)

## Summary
- 2340 nodes · 8444 edges · 118 communities (74 shown, 44 thin omitted)
- Extraction: 91% EXTRACTED · 9% INFERRED · 0% AMBIGUOUS · INFERRED: 749 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- Core API and Types
- Object Content Sources
- Drive Connection Registry
- S3 Fallback Provider
- Drive OAuth Flow
- Configuration Test Suite
- Upload Verification Tests
- Problem Detail Errors
- SDK Models and Errors
- Connection and Consent
- Console Embedding
- Service Configuration
- HTTP Auth Middleware
- Routing Failure Policy
- Development Launcher
- Configuration Loading
- Storage Contract and Policy
- Go SDK Client Tests
- HTTP Handlers
- Structured Logging
- Drive Upload Sessions
- TypeScript Package
- Upload Routing
- Secret Boundary Tests
- Fallback Consent
- Redis Sessions
- Drive Files and Folders
- Console Operations
- Persistence Repositories
- Live Console Tests
- Encryption Keyring
- TypeScript Compiler Config
- Console Dependencies
- Console UI Shell
- Session Authentication
- Auth Issuer
- Environment Files
- SDK Object Operations
- Rate Limiting
- Application Lifecycle
- Fallback Uploads
- Console Upload API
- Console TypeScript Config
- Runtime State
- Backend Repositories
- HTTP Server Handlers
- Application Wiring
- PostgreSQL Schema
- SQLite Schema
- Upload Integration Tests
- Metrics Tests
- Database Store
- Job Repository
- Object Deletion
- Drive Connection State
- Routing Switch
- Memory Cache
- Redis Cache
- SDK API Errors
- Range Downloads
- Fault Injection
- Metrics Registry
- Fallback Consent Repository
- Drive Changes Poller
- Background Workers
- Console Pages
- Object Table UI
- SDK Drive Operations
- SDK Upload Transfers
- SDK Storage API
- Console Upload Service
- Drive Backend Wiring
- Database Migrations
- Metrics Primitives
- Metric Registry
- Memory Cache Internals
- Metrics Formatting
- Metric Vectors
- Console OAuth
- OAuth State Store
- HTTP Client Transport
- Console API State
- Provider Error Stubs
- Memory Sessions
- Live Provider Verification
- Migration Backfill
- Storage Status Views
- Secret Configuration Boundary
- Metric Label Registry
- Console OAuth Flow
- Error Classification
- Console App Layout
- Embedded Assets
- Fault Switch
- Infrastructure Verification Gaps
- HTTP Response Recorder
- ID Generator
- Postgres Consent Migration
- SQLite Consent Migration
- Frontend Build Toolchain
- Verification Status
- Drive Folder Recovery
- Testing Console
- Global Error Page
- Next.js Configuration
- Provider Adapter Boundary
- Redis Shared State
- Job Queue and Leases
- HTTP Routing Choice
- Problem Details Format
- SDK Idempotency Keys
- Console Agent Rules
- Metrics Status
- Account Profile Migration
- Drive Connections Migration
- Deleted At Backfill

## God Nodes (most connected - your core abstractions)
1. `Scope` - 147 edges
2. `testScope()` - 47 edges
3. `newTestService()` - 45 edges
4. `Config` - 44 edges
5. `Client` - 43 edges
6. `LogicalObject` - 39 edges
7. `Client` - 37 edges
8. `validRequest()` - 37 edges
9. `Console` - 36 edges
10. `newFileTestClient()` - 36 edges

## Surprising Connections (you probably didn't know these)
- `Recorded Go and SDK checks` --conceptually_related_to--> `README implementation status and verification gaps`  [INFERRED]
  docs/implementation-plan.md → README.md
- `TestErrorDecodingUsesTheProblemType()` --calls--> `StatusCode()`  [INFERRED]
  sdk/go/client_test.go → sdk/go/client.go
- `Redis shared session and circuit state` --conceptually_related_to--> `Redis live verification gap`  [INFERRED]
  docker-compose.yml → docs/implementation-status.md
- `Drive-first normal uploads` --conceptually_related_to--> `README Drive-first one-copy summary`  [INFERRED]
  docs/storage-policy.md → README.md
- `One application-managed location per logical object` --conceptually_related_to--> `README Drive-first one-copy summary`  [INFERRED]
  docs/storage-policy.md → README.md

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Drive-first routing with outage-only fallback and one saved location** — docs_storage_policy_drive_first_contract, docs_storage_policy_outage_only_fallback, docs_storage_policy_single_location_contract, readme_storage_contract_summary, docs_implementation_plan_confirmed_storage_requirements, changelog_live_drive_r2_verification [EXTRACTED 1.00]
- **Ambiguous writes remain pinned and are reconciled without re-upload** — docs_storage_policy_ambiguous_write_reconciliation, docs_storage_policy_timeout_not_failure_rationale, docs_storage_policy_same_provider_pin_rationale, docs_architecture_preflight_availability_hint, docs_implementation_plan_reconciliation_gap, changelog_reconciliation_and_no_retry [INFERRED 0.95]
- **Versioned disclosure gates tenant-specific fallback storage** — docs_storage_policy_fallback_consent_disclosure, docs_storage_policy_privacy_scope_rationale, docs_architecture_runtime_consent_evaluation, readme_consent_behavior, docs_implementation_plan_privacy_approval_gap, changelog_consent_disclosure_implementation [INFERRED 0.95]

## Communities (118 total, 44 thin omitted)

### Community 0 - "Core API and Types"
Cohesion: 0.09
Nodes (71): contextKey, AdminPanelConfig, Scope, driveCompleteResponse, connectionResponse, createUploadRequest, errorResponse, Problem (+63 more)

### Community 1 - "Object Content Sources"
Cohesion: 0.05
Nodes (15): ConnectionResolver, Store, storeResolver, stubResolver, DrivePort, stubDrive, unavailableDrive, ConnectionProfile (+7 more)

### Community 2 - "Drive Connection Registry"
Cohesion: 0.06
Nodes (39): Generator, contains(), ContentType(), copyLabels(), escapeLabel(), formatLabels(), labelKey(), NewRegistry() (+31 more)

### Community 3 - "S3 Fallback Provider"
Cohesion: 0.04
Nodes (71): TestConfigRefusesFaultInjectionOutsideDevelopment(), TestConstructorsUseTheirOwnKind(), TestInternalDetailIsGeneric(), TestKindStatusMappingIsTotal(), TestUncertainIsAnErrorStatus(), TestWrapRetainsTheCauseAndDoesNotExposeIt(), TestResolveSecretsFailsFastOnAMissingSecret(), TestResolveSecretsOnlyRequiresWhatIsEnabled() (+63 more)

### Community 4 - "Drive OAuth Flow"
Cohesion: 0.07
Nodes (48): ExchangeResult, MemoryTokens, responseRecorder, roundTripFunc, tokenEntry, isExpiredPageToken(), parseTokenResponse(), ChunkSizeIsValid() (+40 more)

### Community 5 - "Configuration Test Suite"
Cohesion: 0.08
Nodes (60): apiHarness, newConsoleStore(), containsJSONField(), createDriveUpload(), newAPIHarness(), pathValueFor(), request(), TestCompleteForAFallbackUploadCommits() (+52 more)

### Community 6 - "Upload Verification Tests"
Cohesion: 0.08
Nodes (28): Authentication(), Authorization(), Conflict(), ConsentRequired(), Error, Kind, Internal(), New() (+20 more)

### Community 7 - "Problem Detail Errors"
Cohesion: 0.07
Nodes (14): consentController, Connection, AdminConnections, ConsoleOptions, FallbackConsenter, FolderEnsurer, stubConnector, Scope (+6 more)

### Community 8 - "SDK Models and Errors"
Cohesion: 0.12
Nodes (22): Config, ConnectionHandler, ConsentHandler, HealthHandler, ObjectHandler, objectResponse, UploadHandler, TestWithFieldDoesNotMutateTheReceiver() (+14 more)

### Community 9 - "Connection and Consent"
Cohesion: 0.06
Nodes (44): Interactive browser consent remains unverified, Versioned fallback disclosure implemented, Current live verification gaps, Storvia change log, Large chunked Drive upload remains unverified, Live Drive and R2 paths verified, OAuth state-bound diagnostic flow, PostgreSQL and Redis remain unproven (+36 more)

### Community 10 - "Console Embedding"
Cohesion: 0.07
Nodes (24): CompleteResponse, CreateUploadRequest, CreateUploadResult, DriveConnection, DriveSession, ErrorKind, kindForStatus(), kindFromProblemType() (+16 more)

### Community 11 - "Service Configuration"
Cohesion: 0.14
Nodes (39): apply_flag_override(), build(), claim_port(), cmd_logs(), cmd_start(), cmd_status(), cmd_stop(), CONFIG_FILE (+31 more)

### Community 12 - "HTTP Auth Middleware"
Cohesion: 0.09
Nodes (16): Worker, WorkerConfig, NewWorker(), StartWorker(), Job, JobRepository, newJobID(), NewJobRepository() (+8 more)

### Community 13 - "Routing Failure Policy"
Cohesion: 0.14
Nodes (39): TestPostgresRequiresEveryOrdinaryParameter(), TestPostgresRequiresTLSInProduction(), dedupe(), ExpandEnv(), Load(), Parse(), ids(), lookupMap() (+31 more)

### Community 14 - "Development Launcher"
Cohesion: 0.09
Nodes (22): entry, Memory, MemoryOption, Redis, RedisOption, buildCache(), NewMemory(), TestMemoryCloseIsIdempotent() (+14 more)

### Community 15 - "Configuration Loading"
Cohesion: 0.09
Nodes (17): consentAccepter, AccountInfo, ConsentInfo, FaultInfo, State, FallbackConsent, ConsentGate(), DefaultFallbackDisclosure() (+9 more)

### Community 16 - "Storage Contract and Policy"
Cohesion: 0.08
Nodes (15): AuthConfig, Duration, LoggingConfig, S3BackendConfig, SecretRef, SecurityConfig, ServerConfig, ServiceConfig (+7 more)

### Community 17 - "Go SDK Client Tests"
Cohesion: 0.09
Nodes (13): seedObject(), checkAffected(), coerceBool(), AttemptRepository, ObjectRepository, isUniqueViolation(), nullable(), nullablePtr() (+5 more)

### Community 18 - "HTTP Handlers"
Cohesion: 0.09
Nodes (20): faultToggle, ClientDeps, Config, Credentials, TokenCache, buildBackends(), buildDrive(), buildServices() (+12 more)

### Community 19 - "Structured Logging"
Cohesion: 0.07
Nodes (32): parseRangeImpl(), requestIDFromContext(), decodeProblem(), parseRangeForTest(), TestAccessLogDoesNotLogQueryStrings(), TestContentDispositionEscapesInjection(), TestContentRangeFormatting(), TestCORSEchoesOnlyAllowedOrigins() (+24 more)

### Community 20 - "Drive Upload Sessions"
Cohesion: 0.11
Nodes (26): buildKeyring(), keyringReferences(), base64Decode(), hexDecode(), StringRef(), decodeKey(), AESGCMKeyring, NewAESGCMKeyring() (+18 more)

### Community 21 - "TypeScript Package"
Cohesion: 0.11
Nodes (25): loggerWithCorrelationID(), loggingContext(), CorrelationID(), IsSensitive(), New(), redactAttr(), RedactHandler(), decode() (+17 more)

### Community 22 - "Upload Routing"
Cohesion: 0.12
Nodes (9): ConsentController, RoutingControl, accountLabel(), Console, safeConsoleDetail(), sanitizeFilename(), targetProvider(), TestAccountLabelFallsBackInOrder() (+1 more)

### Community 23 - "Secret Boundary Tests"
Cohesion: 0.10
Nodes (24): idempotencyKeyContext, Middleware, RateLimiter, statusRecorder, window, TestRateLimiterBlocksAfterTheLimit(), TestRateLimiterWindowResets(), TestRecoveryConvertsPanicToInternalError() (+16 more)

### Community 24 - "Fallback Consent"
Cohesion: 0.10
Nodes (20): Session, buildAuth(), buildSessionStore(), authIdentity(), BearerToken(), Identity, Issuer, Lookup (+12 more)

### Community 25 - "Redis Sessions"
Cohesion: 0.06
Nodes (30): bugs, url, description, devDependencies, @types/node, typescript, engines, node (+22 more)

### Community 26 - "Drive Files and Folders"
Cohesion: 0.08
Nodes (16): App, consentLooker, diagnostics, AdminBackendStatus, buildServer(), explainProviderError(), newDiagnostics(), Cache (+8 more)

### Community 27 - "Console Operations"
Cohesion: 0.11
Nodes (25): stubRouting, NewMemorySessions(), TestConsoleHandbackFallsBackToTheLocalConsole(), TestOAuthIsEntirelyBackendLogic(), TestSignOutClearsTheCookieOnTheBackend(), TestSignOutRedirectsToTheConsole(), consoleRouter(), TestCallbackWithNoCodeDoesNot404() (+17 more)

### Community 28 - "Persistence Repositories"
Cohesion: 0.18
Nodes (25): Options, Build(), releaseVersion(), TestBuildWiresTheOutageFallbackDependencies(), buildLiveApp(), buildTestConsole(), consoleHandler(), doGET() (+17 more)

### Community 29 - "Live Console Tests"
Cohesion: 0.09
Nodes (7): AccountProfile, FileMetadata, FolderState, InitiateUploadRequest, InitiateUploadResult, drainAndClose(), summariseGoogleError()

### Community 30 - "Encryption Keyring"
Cohesion: 0.07
Nodes (24): metadata, dependencies, next, react, react-dom, devDependencies, @types/node, @types/react (+16 more)

### Community 31 - "TypeScript Compiler Config"
Cohesion: 0.14
Nodes (23): IsKind(), New(), KindOfError(), newTestClient(), TestBearerTokenIsSent(), TestContextCancellationIsHonoured(), TestCreateUploadRequiresAnIdempotencyKey(), TestCreateUploadSendsTheIdempotencyKey() (+15 more)

### Community 32 - "Console Dependencies"
Cohesion: 0.21
Nodes (5): ProviderObject, awsInt32(), awsInt64(), awsString(), Client

### Community 33 - "Console UI Shell"
Cohesion: 0.17
Nodes (26): decision(), Evaluate(), evaluateUnavailable(), RoutingDecision, NewCircuit(), reject(), allGatesOpen(), gatesWith() (+18 more)

### Community 34 - "Session Authentication"
Cohesion: 0.10
Nodes (10): failingClient, RedisSessionOption, RedisSessions, NewRedisSessions(), TestRedisSessionsReportsBackendFailureDistinctly(), TestSessionKeysAreNamespaced(), TestSessionStoreMustHaveAnID(), TestSessionTTLDefaultsSensibly() (+2 more)

### Community 35 - "Auth Issuer"
Cohesion: 0.10
Nodes (26): TestValidateSecretRefsAcceptsVariableNames(), TestValidateSecretRefsErrorDoesNotEchoTheSecret(), TestValidateSecretRefsRejectsACredentialInConfig(), TestValidateSecretRefsRequiresAReferenceWhenRequired(), looksLikeCredential(), redactIfCredential(), TestCheckedInJSONHasNoSecretFields(), TestSecretEnvVarNamesAreCanonical() (+18 more)

### Community 36 - "Environment Files"
Cohesion: 0.11
Nodes (11): NewUploadHandler(), BackendRepository, fingerprint(), CreateUploadResult, Dependencies, DriveAvailability, UploadService, Backend (+3 more)

### Community 37 - "SDK Object Operations"
Cohesion: 0.08
Nodes (23): compilerOptions, allowImportingTsExtensions, declaration, declarationMap, exactOptionalPropertyTypes, forceConsistentCasingInFileNames, isolatedModules, lib (+15 more)

### Community 38 - "Rate Limiting"
Cohesion: 0.18
Nodes (7): contentSource, stubContentSource, providerObject(), ByteRange, LogicalObject, ContentStream, fakeSource

### Community 39 - "Application Lifecycle"
Cohesion: 0.12
Nodes (13): MemorySessions, ConnectionStatusInfo, DriveSession, Connection, createUploadResponse, ObjectView, nowForTest(), sessionIDFromToken() (+5 more)

### Community 40 - "Fallback Uploads"
Cohesion: 0.23
Nodes (15): Error(), NotFound(), Console(), DrivePanel(), Props, OperatorControls(), Props, RoutingSummary() (+7 more)

### Community 41 - "Console Upload API"
Cohesion: 0.16
Nodes (20): ComputeSHA256Base64(), New(), classifyLike(), TestBuildKeyIsServerDerivedAndTenantScoped(), TestBuildKeySanitizesSegments(), TestComputeSHA256Base64(), TestConfigValidationRejectsUnsafeSettings(), TestMultipartTTLIsReported() (+12 more)

### Community 42 - "Console TypeScript Config"
Cohesion: 0.19
Nodes (15): Page(), AccountMenu(), Avatar(), ConsoleGate(), SignInGate(), API_BASE, API_ORIGIN, ApiError (+7 more)

### Community 43 - "Runtime State"
Cohesion: 0.10
Nodes (3): completeResponse, Object, Upload

### Community 44 - "Backend Repositories"
Cohesion: 0.10
Nodes (11): classifySDKError(), httpStatusFromError(), isTimeout(), sanitizeSegment(), TestClassifySDKErrorExtractsCodeAndStatus(), CompletedPart, Config, Credentials (+3 more)

### Community 45 - "HTTP Server Handlers"
Cohesion: 0.22
Nodes (19): IssuerOptions, NewIssuer(), newTestIssuer(), TestClearSessionCookieExpiresIt(), TestCookieHelpers(), testIdentity(), TestIdentityContextRoundTrip(), TestIdentityValid() (+11 more)

### Community 46 - "Application Wiring"
Cohesion: 0.12
Nodes (11): seedDriveConnection(), TestBuildDSNSeparatesSecretsFromOrdinaryConfig(), TestBuildDSNSQLiteUsesThePathAndNoPassword(), DatabaseConfig, Store, normalizeSQLiteDSN(), Open(), rebindToPostgres() (+3 more)

### Community 47 - "PostgreSQL Schema"
Cohesion: 0.13
Nodes (17): EnvFile, ApplyEnvFile(), EnvFileLookup(), FormatEnv(), LoadEnvFile(), ParseEnv(), parseEnvValue(), TestApplyEnvFileDoesNotOverrideTheProcessEnvironment() (+9 more)

### Community 48 - "SQLite Schema"
Cohesion: 0.11
Nodes (18): compilerOptions, allowJs, esModuleInterop, incremental, isolatedModules, jsx, lib, module (+10 more)

### Community 49 - "Upload Integration Tests"
Cohesion: 0.20
Nodes (11): AdminPanelConfig, newFallbackUploader(), Backoff(), RoutingBudget(), TestBackoffIsBoundedAndJittered(), TestRetryBudgetRefusesAttemptLargerThanRemaining(), TestRetryBudgetStopsAttempts(), TestRetryBudgetStopsOnElapsedTime() (+3 more)

### Community 50 - "Metrics Tests"
Cohesion: 0.17
Nodes (8): driveSessions, stubSessions, ObjectMetadata, FileMetadataToModel(), DriveSessionPort, DriveSessionResult, DriveUploadRequest, fakeDriveSessions

### Community 51 - "Database Store"
Cohesion: 0.18
Nodes (11): APIError, Download, Kind, problem, requestOption, decodeProblem(), kindForStatus(), kindFromProblemType() (+3 more)

### Community 52 - "Job Repository"
Cohesion: 0.15
Nodes (8): Chunk, Progress, Session, UploadMode, UploadState, Availability, ModeSelector, Storage

### Community 53 - "Object Deletion"
Cohesion: 0.16
Nodes (7): fallbackUploader, stubUploader, FallbackUploader, VerifiedObject, PresignedPartInfo, mismatchingFallback, stubFallback

### Community 54 - "Drive Connection State"
Cohesion: 0.15
Nodes (9): CreateUploadRequest, CreateUploadResult, DriveConnection, PresignedPart, ReconcileResult, startConnectionResponse, StorageStatus, UploadMode (+1 more)

### Community 55 - "Routing Switch"
Cohesion: 0.14
Nodes (3): Client, Config, safeRedirect()

### Community 56 - "Memory Cache"
Cohesion: 0.20
Nodes (8): UploadResult, deref(), drainAndClose(), firstNonEmpty(), TestParseDriveUploadResponse(), parseDriveUploadResponse(), copyBounded(), readerFromString()

### Community 57 - "Redis Cache"
Cohesion: 0.34
Nodes (7): RoutingSwitch, boolPtr(), NewRoutingSwitch(), TestNilRoutingSwitchIsSafe(), TestRoutingSwitchClearRestoresConfig(), TestRoutingSwitchFollowsConfigByDefault(), TestRoutingSwitchOverrideBeatsConfig()

### Community 58 - "SDK API Errors"
Cohesion: 0.24
Nodes (14): containsAll(), makeReady(), newObjectService(), TestDeleteAllObjectsClearsEveryStoredObject(), TestDeleteAllObjectsReportsFailuresWithoutStopping(), TestDeleteIsIdempotent(), TestDeleteIsTenantScoped(), TestDeleteRemovesFromTheRecordedProvider() (+6 more)

### Community 59 - "Range Downloads"
Cohesion: 0.28
Nodes (12): messageOf(), ObjectTable(), refresh(), remove(), removeAll(), providerLabel(), referenceOf(), STATUS (+4 more)

### Community 60 - "Fault Injection"
Cohesion: 0.23
Nodes (6): FaultFlag, FaultInjection, TestFaultInjectionIsGatedOnTheEnvironment(), NewFaultFlag(), NewFaultInjection(), NewFaultInjectionWithFlag()

### Community 61 - "Metrics Registry"
Cohesion: 0.24
Nodes (4): UploadRepository, ObjectStatus, Upload, UploadView

### Community 62 - "Fallback Consent Repository"
Cohesion: 0.20
Nodes (9): Props, api, BulkDeleteFailure, Connection, FallbackDisclosure, ObjectView, RoutingInfo, StorageInfo (+1 more)

### Community 63 - "Drive Changes Poller"
Cohesion: 0.31
Nodes (8): sessionRecorder, startDriveUpload(), TestCompleteDriveRequiresAFileID(), TestCompleteDriveSendsTheFileID(), TestCompleteRefusesADriveUpload(), TestUploadAllHandlesAFileSmallerThanOneChunk(), TestUploadAllReportsAFailedChunkWithoutLosingTheOffset(), TestUploadAllSendsEveryChunkWithItsRange()

### Community 64 - "Background Workers"
Cohesion: 0.22
Nodes (8): consoleFromDisk(), loadConsoleAssets(), mountConsole(), loadConsoleAssets(), withConsoleAssets(), testConsoleAssets(), TestConsoleAssetsAreImmutable(), TestConsoleServesTheDocumentSignedOut()

### Community 65 - "Console Pages"
Cohesion: 0.31
Nodes (4): stateEntry, StateStore, constantTimeEqual(), hashSecret()

### Community 66 - "Object Table UI"
Cohesion: 0.25
Nodes (4): LoadMigrations(), parseMigrationName(), splitStatements(), Migration

### Community 67 - "SDK Drive Operations"
Cohesion: 0.25
Nodes (7): ClassifyGoogleError(), ClassifyS3Error(), FailureClass, ProviderError, TestClassifyGoogleError(), TestClassifyOnlyUnavailableMapsToFallbackAcrossBothProviders(), TestClassifyS3Error()

### Community 68 - "SDK Upload Transfers"
Cohesion: 0.36
Nodes (5): ChangesPoller, ChangePage, countRemoved(), StartChangesPoller(), Change

### Community 70 - "Console Upload Service"
Cohesion: 0.48
Nodes (6): countStamped(), exec(), idStatusFor(), openStoreAtVersion(), scanOne(), TestBackfillMigrationStampsPreviouslyDeletedObjects()

### Community 72 - "Database Migrations"
Cohesion: 0.40
Nodes (5): OAuth state and redirect integrity, Capability leakage risk, Tenant isolation risk, High-severity security scope, Private vulnerability disclosure

### Community 74 - "Metric Registry"
Cohesion: 0.60
Nodes (3): FaultStore, FaultSwitch, NewFaultSwitch()

### Community 75 - "Memory Cache Internals"
Cohesion: 0.60
Nodes (5): CanTransition(), TestCanTransition(), TestReadyHasNoTransitionBackToUploading(), ValidateTransition(), ObjectState

### Community 76 - "Metrics Formatting"
Cohesion: 0.50
Nodes (4): Live Drive verification status, Research source limitations, Pilot go or no-go gate, Pre-release verification status

## Knowledge Gaps
- **75 isolated node(s):** `react-dom`, `@types/node`, `@types/react`, `@types/react-dom`, `typescript` (+70 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 351 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **44 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Scope` connect `Problem Detail Errors` to `Core API and Types`, `Object Content Sources`, `Drive OAuth Flow`, `Upload Verification Tests`, `SDK Models and Errors`, `HTTP Auth Middleware`, `Configuration Loading`, `Go SDK Client Tests`, `Upload Routing`, `Fallback Consent`, `Drive Files and Folders`, `Persistence Repositories`, `Live Console Tests`, `Environment Files`, `Rate Limiting`, `Application Wiring`, `Metrics Tests`, `Memory Cache`, `SDK API Errors`, `Metrics Registry`?**
  _High betweenness centrality (0.041) - this node is a cross-community bridge._
- **What connects `react-dom`, `@types/node`, `@types/react` to the rest of the system?**
  _75 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Core API and Types` be split into smaller, more focused modules?**
  _Cohesion score 0.08932840686807215 - nodes in this community are weakly interconnected._
- **Why does `assertGranted()` connect `Configuration Test Suite` to `Core API and Types`, `Object Content Sources`, `S3 Fallback Provider`?**
  _High betweenness centrality (0.026) - this node is a cross-community bridge._
- **Should `Object Content Sources` be split into smaller, more focused modules?**
  _Cohesion score 0.045709908069458634 - nodes in this community are weakly interconnected._
- **Why does `Config` connect `Storage Contract and Policy` to `Core API and Types`, `Drive Backend Wiring`, `Routing Failure Policy`, `Application Wiring`, `HTTP Handlers`, `Drive Upload Sessions`, `Fallback Consent`, `Drive Files and Folders`, `Persistence Repositories`?**
  _High betweenness centrality (0.016) - this node is a cross-community bridge._
- **Should `Drive Connection Registry` be split into smaller, more focused modules?**
  _Cohesion score 0.05822784810126582 - nodes in this community are weakly interconnected._