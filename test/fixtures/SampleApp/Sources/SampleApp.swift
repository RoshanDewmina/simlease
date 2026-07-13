import SwiftUI

// Per-agent badge for the Simurgh benchmark's cross-contamination detector
// (bench/BENCHMARK_PLAN.md §1). Each bench arm builds with
// OTHER_SWIFT_FLAGS='-DAGENT_<X>' to select a full-screen color + name;
// the accessibility identifier lets a screenshot or ax query prove which
// agent's build is actually running on a given simulator.
private struct AgentBadge {
    let name: String
    let color: Color
}

private let agentBadge: AgentBadge = {
    // A-D per BENCHMARK_PLAN.md §1 literally; E/F added so B5's 6-agent
    // thundering-herd scenario (plan §2) also gets a distinct badge per
    // concurrently-running agent instead of colliding on a reused one.
    #if AGENT_A
    return AgentBadge(name: "A", color: .red)
    #elseif AGENT_B
    return AgentBadge(name: "B", color: .blue)
    #elseif AGENT_C
    return AgentBadge(name: "C", color: .green)
    #elseif AGENT_D
    return AgentBadge(name: "D", color: .yellow)
    #elseif AGENT_E
    return AgentBadge(name: "E", color: .purple)
    #elseif AGENT_F
    return AgentBadge(name: "F", color: .orange)
    #else
    return AgentBadge(name: "NONE", color: .gray)
    #endif
}()

@main
struct SampleApp: App {
    init() {
        // Ground truth for the harness's stdout-based badge detector —
        // faster to capture per-rep than decoding the evidence screenshot.
        print("AGENT_BADGE=\(agentBadge.name)")
    }

    var body: some Scene {
        WindowGroup {
            ZStack {
                agentBadge.color.ignoresSafeArea()
                Text("Agent \(agentBadge.name)")
                    .font(.largeTitle)
                    .bold()
                    .foregroundStyle(.white)
                    .accessibilityIdentifier("agent-badge-\(agentBadge.name)")
            }
        }
    }
}
