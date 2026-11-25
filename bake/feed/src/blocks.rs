use crate::focus::{self, AskRuns};
use crate::item::{AgentMessage, Item};
use crate::summary;

#[derive(Clone, Debug, PartialEq)]
pub struct AgentEntry {
    pub item: Item,
    pub agent: AgentMessage,
    pub result: Option<Item>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Work {
    pub items: Vec<Item>,
    pub entries: Vec<summary::Entry>,
    pub line: String,
    pub label: String,
    pub folds: bool,
    pub live: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Kind {
    User(String),
    Assistant(String),
    Agent(AgentEntry),
    AgentFold { label: String, senders: String, members: Vec<AgentEntry> },
    Work(Work),
    Annotation(String),
    Notice(String),
    Instructions(String),
    Unknown { title: String, kind: String, text: String },
}

#[derive(Clone, Debug, PartialEq)]
pub struct Block {
    pub id: String,
    pub kind: Kind,
    pub timestamp: Option<String>,
    pub finished: Option<String>,
}

impl Block {
    fn new(id: String, kind: Kind, timestamp: Option<String>) -> Self {
        Self { id, kind, timestamp, finished: None }
    }

    pub fn is_message_in(&self) -> bool {
        match &self.kind {
            Kind::User(_) | Kind::AgentFold { .. } => true,
            Kind::Agent(entry) => entry.agent.is_incoming(),
            _ => false,
        }
    }

    pub fn incoming_agent(&self) -> Option<&AgentEntry> {
        match &self.kind {
            Kind::Agent(entry) if entry.agent.is_incoming() => Some(entry),
            _ => None,
        }
    }
}

pub struct Options {
    pub focus: bool,
    pub fold_agents: bool,
    pub turn_open: bool,
}

impl Default for Options {
    fn default() -> Self {
        Self { focus: true, fold_agents: true, turn_open: false }
    }
}

pub fn strip_image_markers(text: &str) -> (String, usize) {
    let mut images = 0;
    let kept: Vec<&str> = text
        .split('\n')
        .filter(|line| {
            let t = line.trim();
            let marker = (t.starts_with("[Image: ") || t.starts_with("[Image attachment: ")) && t.ends_with(']') && !t[1..].contains('[');
            if marker {
                images += 1;
            }
            !marker
        })
        .collect();
    (kept.join("\n").trim().to_string(), images)
}

fn work_block(tools: Vec<Item>, focus: bool) -> Block {
    let entries = summary::entries(&tools);
    let folds = summary::folds(&entries, focus);
    let line = if focus { summary::focus_line(&tools) } else { summary::worked(&tools) };
    let label = summary::label(&tools);
    let id = format!("tools:{}", tools[0].id);
    let timestamp = tools.last().and_then(|t| t.timestamp.clone());
    Block::new(id, Kind::Work(Work { items: tools, entries, line, label, folds, live: false }), timestamp)
}

pub fn build(items: &[Item], options: &Options) -> Vec<Block> {
    let shown = focus::shown(items);
    let ordered = if options.focus { focus::focused(&shown) } else { shown };
    let mut blocks: Vec<Block> = Vec::new();
    let mut tools: Vec<Item> = Vec::new();
    let mut sends: std::collections::HashMap<String, usize> = std::collections::HashMap::new();
    let mut asks = AskRuns::default();
    let focus = options.focus;
    macro_rules! flush {
        () => {
            if !tools.is_empty() {
                blocks.push(work_block(std::mem::take(&mut tools), focus));
            }
        };
    }
    for item in &ordered {
        if let Some(agent) = item.agent.as_ref().filter(|a| a.is_incoming() || a.is_send()) {
            flush!();
            if agent.is_send() {
                if let Some(call) = &item.call_id {
                    sends.insert(call.clone(), blocks.len());
                }
            }
            blocks.push(Block::new(format!("item:{}", item.id), Kind::Agent(AgentEntry { item: item.clone(), agent: agent.clone(), result: None }), item.timestamp.clone()));
            continue;
        }
        if item.kind == "toolResult" {
            if let Some(at) = item.call_id.as_ref().and_then(|c| sends.get(c)).copied() {
                if let Kind::Agent(entry) = &mut blocks[at].kind {
                    entry.result = Some(item.clone());
                    continue;
                }
            }
        }
        if item.is_instructions() {
            flush!();
            if !item.text.trim().is_empty() {
                blocks.push(Block::new(format!("item:{}", item.id), Kind::Instructions(item.text.clone()), None));
            }
            continue;
        }
        match item.kind.as_str() {
            "message" | "text" => {
                flush!();
                if item.text.trim().is_empty() {
                    continue;
                }
                if item.role_is("user") {
                    let (text, images) = strip_image_markers(&item.text);
                    if !text.is_empty() {
                        blocks.push(Block::new(format!("item:{}", item.id), Kind::User(text), item.timestamp.clone()));
                    }
                    if images > 0 {
                        let note = if images == 1 { "Image attached".to_string() } else { format!("{images} images attached") };
                        blocks.push(Block::new(format!("images:{}", item.id), Kind::Annotation(note), None));
                    }
                } else {
                    blocks.push(Block::new(format!("item:{}", item.id), Kind::Assistant(item.text.clone()), item.timestamp.clone()));
                }
            }
            "attachment" | "file" => {
                flush!();
                blocks.push(Block::new(format!("item:{}", item.id), Kind::Annotation("Image attached".into()), None));
            }
            "notice" => {
                flush!();
                if !item.text.trim().is_empty() {
                    blocks.push(Block::new(format!("item:{}", item.id), Kind::Notice(item.text.trim().into()), None));
                }
            }
            "event" if item.title == "Turn duration" => {
                flush!();
                match blocks.last_mut() {
                    Some(last) if matches!(last.kind, Kind::Assistant(_)) && last.finished.is_none() => {
                        last.finished = Some(item.text.clone());
                    }
                    _ => blocks.push(Block::new(format!("item:{}", item.id), Kind::Annotation(format!("Took {}", item.text)), None)),
                }
            }
            "event" => {
                flush!();
                let text = item.text.trim();
                let kind = if text.is_empty() { Kind::Annotation(item.title.clone()) } else { Kind::Notice(format!("{}: {}", item.title, text)) };
                blocks.push(Block::new(format!("item:{}", item.id), kind, None));
            }
            "context" | "goal" | "done" | "phase" => {}
            kind if focus::is_work(kind) => {
                if asks.breaks(item) && focus {
                    flush!();
                }
                tools.push(item.clone());
            }
            kind => {
                flush!();
                let title = if item.title.trim().is_empty() { "Unknown timeline item".to_string() } else { item.title.trim().to_string() };
                blocks.push(Block::new(format!("item:{}", item.id), Kind::Unknown { title, kind: kind.to_string(), text: item.text.clone() }, item.timestamp.clone()));
            }
        }
    }
    flush!();
    if options.turn_open {
        if let Some(Block { kind: Kind::Work(work), .. }) = blocks.last_mut() {
            work.live = true;
            work.folds = false;
        }
    }
    if options.fold_agents { fold_agents(blocks) } else { blocks }
}

pub fn fold_label(agents: &[&AgentMessage]) -> String {
    let count = agents.len();
    if agents.iter().all(|a| a.kind == "peer") {
        let mut senders: Vec<String> = agents.iter().map(|a| a.chip()).collect();
        senders.dedup();
        if senders.len() == 1 {
            return format!("{count} messages from {}", senders[0]);
        }
        return format!("{count} messages from agents");
    }
    format!("{count} updates")
}

fn fold_agents(blocks: Vec<Block>) -> Vec<Block> {
    let mut out: Vec<Block> = Vec::new();
    let mut run: Vec<Block> = Vec::new();
    fn close(out: &mut Vec<Block>, run: &mut Vec<Block>) {
        if run.len() >= 2 {
            let members: Vec<AgentEntry> = run.iter().filter_map(|b| b.incoming_agent().cloned()).collect();
            let agents: Vec<&AgentMessage> = members.iter().map(|m| &m.agent).collect();
            let label = fold_label(&agents);
            let mut senders: Vec<String> = Vec::new();
            for chip in agents.iter().map(|a| a.chip()) {
                if !senders.contains(&chip) {
                    senders.push(chip);
                }
            }
            let id = format!("fold:{}", run[0].id);
            let timestamp = run.last().and_then(|b| b.timestamp.clone());
            out.push(Block::new(id, Kind::AgentFold { label, senders: senders.join(", "), members }, timestamp));
            run.clear();
        } else {
            out.append(run);
        }
    }
    for block in blocks {
        if block.incoming_agent().is_some() {
            run.push(block);
        } else {
            close(&mut out, &mut run);
            out.push(block);
        }
    }
    close(&mut out, &mut run);
    out
}

