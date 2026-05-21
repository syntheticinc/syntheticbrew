# Flutter Developer Memory

## Testing Patterns
- Tests use Fake implementations (not mocks) for gRPC clients -- see `FakeMobileServiceClient` in `connection_manager_test.dart`
- `ConnectionManager` accepts `clientFactory` parameter for injecting fakes in tests
- To test gRPC repositories, connect a fake server via `connectionManager.connectToServer()` then call the repository methods
- Stream-based tests: use `StreamController.broadcast()` in fakes, add events, then `await Future<void>.delayed(Duration.zero)` to let listeners process
- `GrpcPairingRepository` creates its own channels/clients internally -- cannot inject fakes at client level, test mapping logic separately

## Flutter/Dart CLI Issues
- `dart analyze`, `flutter test`, `dart run build_runner` do NOT work in Git Bash on Windows -- they hang indefinitely
- Never run these commands in sub-agents or bash tool
- Write `.g.dart` files manually instead of running build_runner
- Ask user to run verification commands

## Key File Paths
- Domain entities: `syntheticbrew-mobile-app/lib/core/domain/`
- gRPC client wrapper: `syntheticbrew-mobile-app/lib/core/infrastructure/grpc/mobile_service_client.dart`
- Connection manager: `syntheticbrew-mobile-app/lib/core/infrastructure/grpc/connection_manager.dart`
- Test fixtures: `syntheticbrew-mobile-app/test/fixtures/`
- Test helpers: `syntheticbrew-mobile-app/test/helpers/`

## Architecture Notes
- Feature-first Clean Architecture: `features/<name>/{domain,infrastructure,presentation}/`
- Shared entities in `core/domain/`
- gRPC DTOs (SessionEvent, payloads) defined in `mobile_service_client.dart`, NOT in proto-generated files
- `MobileServiceClient` wraps proto-generated `MobileServiceGrpcClient` and converts proto -> typed Dart DTOs
