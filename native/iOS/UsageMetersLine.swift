import SwiftUI

struct UsageMetersRow: View {
    @ObservedObject var feed: UsageFeed

    var body: some View {
        if !feed.strip.meters.isEmpty || !feed.strip.others.isEmpty {
            UsageMetersLine(strip: feed.strip, now: feed.now)
                .listRowInsets(EdgeInsets(top: 0, leading: 4, bottom: 4, trailing: 4))
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
        }
    }
}

struct UsageMetersLine: View {
    let strip: UsageStrip
    let now: Date

    var body: some View {
        Group {
            if let problem = strip.problem {
                VStack(alignment: .leading, spacing: 4) {
                    Text(problem).foregroundStyle(PhonePalette.failed).truncationMode(.tail)
                    ViewThatFits(in: .horizontal) {
                        row(meters: false, details: false, others: true)
                        row(meters: false, details: false, others: true, compact: true)
                    }
                }
            } else {
                ViewThatFits(in: .horizontal) {
                    row(details: true, others: true)
                    row(details: false, others: true)
                    VStack(alignment: .leading, spacing: 4) {
                        row(details: false, others: false)
                        if !strip.others.isEmpty { row(meters: false, details: false, others: true) }
                    }
                    row(details: false, others: false)
                    row(details: false, others: false, first: true)
                }
            }
        }
        .font(.footnote)
        .lineLimit(1)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Usage limits")
        .accessibilityIdentifier("usage-limits")
    }

    private func row(meters: Bool = true, details: Bool, others: Bool, first: Bool = false, compact: Bool = false) -> some View {
        HStack(spacing: 12) {
            ForEach(meters ? (first ? Array(strip.meters.prefix(1)) : strip.meters) : [], id: \.self) { meter in
                let spoken = UsageMeterText.spokenLabel(meter, now: now)
                HStack(spacing: 4) {
                    Text(meter.label).foregroundStyle(PhonePalette.muted)
                    value(details ? UsageMeterText.text(meter, now: now) : UsageMeterText.percent(meter.percent), critical: meter.severity == "critical")
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(spoken)
            }
            if others {
                ForEach(strip.others(compact: compact)) { other in
                    HStack(spacing: 4) {
                        Text(other.title).foregroundStyle(PhonePalette.muted)
                        value(UsageMeterText.percent(other.percent), critical: other.critical)
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(other.spoken)
                }
            }
        }
        .fixedSize(horizontal: true, vertical: false)
    }

    @ViewBuilder
    private func value(_ text: String, critical: Bool) -> some View {
        if critical {
            Text(text).monospacedDigit().foregroundStyle(PhonePalette.failed)
        } else {
            Text(text).monospacedDigit()
        }
    }
}
