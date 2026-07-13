import XCTest

// One-time proof (bench/HANDOFF_BENCH_PROMPT.md step 1) that the per-agent
// badge added for the benchmark's cross-contamination detector is actually
// queryable via accessibility, not just visually distinct.
final class SampleAppUITests: XCTestCase {
    func testAgentBadgeIsQueryable() throws {
        let app = XCUIApplication()
        app.launch()

        let badge = app.staticTexts.matching(
            NSPredicate(format: "identifier BEGINSWITH 'agent-badge-'")
        ).firstMatch
        XCTAssertTrue(
            badge.waitForExistence(timeout: 5),
            "expected an accessibility element with identifier prefix 'agent-badge-'"
        )
    }
}
