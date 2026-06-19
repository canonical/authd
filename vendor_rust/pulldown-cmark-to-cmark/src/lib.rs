#![deny(rust_2018_idioms)]

use std::{
    borrow::{Borrow, Cow},
    collections::HashSet,
    fmt::{self, Write},
    ops::Range,
};

use pulldown_cmark::{
    Alignment as TableAlignment, BlockQuoteKind, Event, HeadingLevel, LinkType, MetadataBlockKind, Tag, TagEnd,
};

mod source_range;
mod text_modifications;

pub use source_range::{
    cmark_resume_with_source_range, cmark_resume_with_source_range_and_options, cmark_with_source_range,
    cmark_with_source_range_and_options,
};
use text_modifications::*;

/// Similar to [Pulldown-Cmark-Alignment][Alignment], but with required
/// traits for comparison to allow testing.
#[derive(Copy, Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Alignment {
    None,
    Left,
    Center,
    Right,
}

impl<'a> From<&'a TableAlignment> for Alignment {
    fn from(s: &'a TableAlignment) -> Self {
        match *s {
            TableAlignment::None => Self::None,
            TableAlignment::Left => Self::Left,
            TableAlignment::Center => Self::Center,
            TableAlignment::Right => Self::Right,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum CodeBlockKind {
    Indented,
    Fenced,
}

/// The state of the [`cmark_resume()`] and [`cmark_resume_with_options()`] functions.
/// This does not only allow introspection, but enables the user
/// to halt the serialization at any time, and resume it later.
#[derive(Clone, Default, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[non_exhaustive]
pub struct State<'a> {
    /// The amount of newlines to insert after `Event::Start(...)`
    pub newlines_before_start: usize,
    /// The lists and their types for which we have seen a `Event::Start(List(...))` tag
    pub list_stack: Vec<Option<u64>>,
    /// The computed padding and prefix to print after each newline.
    /// This changes with the level of `BlockQuote` and `List` events.
    pub padding: Vec<Cow<'a, str>>,
    /// Keeps the current table alignments, if we are currently serializing a table.
    pub table_alignments: Vec<Alignment>,
    /// Keeps the current table headers, if we are currently serializing a table.
    pub table_headers: Vec<String>,
    /// The last seen text when serializing a header
    pub text_for_header: Option<String>,
    /// Is set while we are handling text in a code block
    pub code_block: Option<CodeBlockKind>,
    /// True if the last event was text and the text does not have trailing newline. Used to inject additional newlines before code block end fence.
    pub last_was_text_without_trailing_newline: bool,
    /// True if the last event was a paragraph start. Used to escape spaces at start of line (prevent spurrious indented code).
    pub last_was_paragraph_start: bool,
    /// True if the next event is a link, image, or footnote.
    pub next_is_link_like: bool,
    /// Currently open links
    pub link_stack: Vec<LinkCategory<'a>>,
    /// Currently open images
    pub image_stack: Vec<ImageLink<'a>>,
    /// Keeps track of the last seen heading's id, classes, and attributes
    pub current_heading: Option<Heading<'a>>,
    /// True whenever between `Start(TableCell)` and `End(TableCell)`
    pub in_table_cell: bool,

    /// Keeps track of the last seen shortcut/link
    pub current_shortcut_text: Option<String>,
    /// A list of shortcuts seen so far for later emission
    pub shortcuts: Vec<(String, String, String)>,
    /// Index into the `source` bytes of the end of the range corresponding to the last event.
    ///
    /// It's used to see if the current event didn't capture some bytes because of a
    /// skipped-over backslash.
    pub last_event_end_index: usize,
}

impl State<'_> {
    pub fn is_in_code_block(&self) -> bool {
        self.code_block.is_some()
    }
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum LinkCategory<'a> {
    AngleBracketed,
    Reference {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
        id: Cow<'a, str>,
    },
    Collapsed {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
    Shortcut {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
    Other {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum ImageLink<'a> {
    Reference {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
        id: Cow<'a, str>,
    },
    Collapsed {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
    Shortcut {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
    Other {
        uri: Cow<'a, str>,
        title: Cow<'a, str>,
    },
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Heading<'a> {
    id: Option<Cow<'a, str>>,
    classes: Vec<Cow<'a, str>>,
    attributes: Vec<(Cow<'a, str>, Option<Cow<'a, str>>)>,
}

/// Thea mount of code-block tokens one needs to produce a valid fenced code-block.
pub const DEFAULT_CODE_BLOCK_TOKEN_COUNT: usize = 3;

/// Configuration for the [`cmark_with_options()`] and [`cmark_resume_with_options()`] functions.
/// The defaults should provide decent spacing and most importantly, will
/// provide a faithful rendering of your markdown document particularly when
/// rendering it to HTML.
///
/// It's best used with its `Options::default()` implementation.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Options<'a> {
    pub newlines_after_headline: usize,
    pub newlines_after_paragraph: usize,
    pub newlines_after_codeblock: usize,
    pub newlines_after_htmlblock: usize,
    pub newlines_after_table: usize,
    pub newlines_after_rule: usize,
    pub newlines_after_list: usize,
    pub newlines_after_blockquote: usize,
    pub newlines_after_rest: usize,
    /// The amount of newlines placed after TOML or YAML metadata blocks at the beginning of a document.
    pub newlines_after_metadata: usize,
    /// Token count for fenced code block. An appropriate value of this field can be decided by
    /// [`calculate_code_block_token_count()`].
    /// Note that the default value is `4` which allows for one level of nested code-blocks,
    /// which is typically a safe value for common kinds of markdown documents.
    pub code_block_token_count: usize,
    pub code_block_token: char,
    pub list_token: char,
    pub ordered_list_token: char,
    pub increment_ordered_list_bullets: bool,
    pub emphasis_token: char,
    pub strong_token: &'a str,
}

const DEFAULT_OPTIONS: Options<'_> = Options {
    newlines_after_headline: 2,
    newlines_after_paragraph: 2,
    newlines_after_codeblock: 2,
    newlines_after_htmlblock: 1,
    newlines_after_table: 2,
    newlines_after_rule: 2,
    newlines_after_list: 2,
    newlines_after_blockquote: 2,
    newlines_after_rest: 1,
    newlines_after_metadata: 1,
    code_block_token_count: 4,
    code_block_token: '`',
    list_token: '*',
    ordered_list_token: '.',
    increment_ordered_list_bullets: false,
    emphasis_token: '*',
    strong_token: "**",
};

impl Default for Options<'_> {
    fn default() -> Self {
        DEFAULT_OPTIONS
    }
}

impl Options<'_> {
    pub fn special_characters(&self) -> Cow<'static, str> {
        // These always need to be escaped, even if reconfigured.
        const BASE: &str = "#\\_*<>`|[]";
        if DEFAULT_OPTIONS.code_block_token == self.code_block_token
            && DEFAULT_OPTIONS.list_token == self.list_token
            && DEFAULT_OPTIONS.emphasis_token == self.emphasis_token
            && DEFAULT_OPTIONS.strong_token == self.strong_token
        {
            BASE.into()
        } else {
            let mut s = String::from(BASE);
            s.push(self.code_block_token);
            s.push(self.list_token);
            s.push(self.emphasis_token);
            s.push_str(self.strong_token);
            s.into()
        }
    }
}

/// Serialize a stream of [pulldown-cmark-Events][Event] into a string-backed buffer.
///
/// 1. **events**
///    * An iterator over [`Events`][Event], for example as returned by the [`Parser`][pulldown_cmark::Parser]
/// 1. **formatter**
///    * A format writer, can be a `String`.
/// 1. **state**
///    * The optional initial state of the serialization.
/// 1. **options**
///    * Customize the appearance of the serialization. All otherwise magic values are contained
///      here.
///
/// *Returns* the [`State`] of the serialization on success. You can use it as initial state in the
/// next call if you are halting event serialization.
///
/// *Errors* if the underlying buffer fails (which is unlikely) or if the [`Event`] stream
/// cannot ever be produced by deserializing valid Markdown. Each failure mode corresponds to one
/// of [`Error`]'s variants.
pub fn cmark_resume_with_options<'a, I, E, F>(
    events: I,
    mut formatter: F,
    state: Option<State<'a>>,
    options: Options<'_>,
) -> Result<State<'a>, Error>
where
    I: Iterator<Item = E>,
    E: Borrow<Event<'a>>,
    F: fmt::Write,
{
    let mut state = state.unwrap_or_default();
    let mut events = events.peekable();
    while let Some(event) = events.next() {
        state.next_is_link_like = matches!(
            events.peek().map(Borrow::borrow),
            Some(
                Event::Start(Tag::Link { .. } | Tag::Image { .. } | Tag::FootnoteDefinition(..))
                    | Event::FootnoteReference(..)
            )
        );
        cmark_resume_one_event(event, &mut formatter, &mut state, &options)?;
    }
    Ok(state)
}

/// The error returned by [`cmark_resume_one_event`] and
/// [`cmark_resume_with_source_range_and_options`].
#[derive(Debug)]
pub enum Error {
    FormatFailed(fmt::Error),
    UnexpectedEvent,
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::FormatFailed(e) => e.fmt(f),
            Self::UnexpectedEvent => f.write_str("Unexpected event while reconstructing Markdown"),
        }
    }
}

impl std::error::Error for Error {}

impl From<fmt::Error> for Error {
    fn from(e: fmt::Error) -> Self {
        Self::FormatFailed(e)
    }
}

fn cmark_resume_one_event<'a, E, F>(
    event: E,
    formatter: &mut F,
    state: &mut State<'a>,
    options: &Options<'_>,
) -> Result<(), Error>
where
    E: Borrow<Event<'a>>,
    F: fmt::Write,
{
    use pulldown_cmark::{Event::*, Tag::*};

    let last_was_text_without_trailing_newline = state.last_was_text_without_trailing_newline;
    state.last_was_text_without_trailing_newline = false;
    let last_was_paragraph_start = state.last_was_paragraph_start;
    state.last_was_paragraph_start = false;
    let res = match event.borrow() {
        Rule => {
            consume_newlines(formatter, state)?;
            if state.newlines_before_start < options.newlines_after_rule {
                state.newlines_before_start = options.newlines_after_rule;
            }
            formatter.write_str("---")
        }
        Code(text) => {
            if let Some(shortcut_text) = state.current_shortcut_text.as_mut() {
                shortcut_text.push('`');
                shortcut_text.push_str(text);
                shortcut_text.push('`');
            }
            if let Some(text_for_header) = state.text_for_header.as_mut() {
                text_for_header.push('`');
                text_for_header.push_str(text);
                text_for_header.push('`');
            }

            // (re)-escape `|` when it appears as part of inline code in the
            // body of a table.
            //
            // NOTE: This does not do *general* escaped-character handling
            // because the only character which *requires* this handling in this
            // spot in earlier versions of `pulldown-cmark` is a pipe character
            // in inline code in a table. Other escaping is handled when `Text`
            // events are emitted.
            let text = if state.in_table_cell {
                Cow::Owned(text.replace('|', "\\|"))
            } else {
                Cow::Borrowed(text.as_ref())
            };

            // When inline code has leading and trailing ' ' characters, additional space is needed
            // to escape it, unless all characters are space.
            if text.chars().all(|ch| ch == ' ') {
                write!(formatter, "`{text}`")
            } else {
                // More backticks are needed to delimit the inline code than the maximum number of
                // backticks in a consecutive run.
                let backticks = "`".repeat(max_consecutive_chars(&text, '`') + 1);
                let space = match text.as_bytes() {
                    &[b'`', ..] | &[.., b'`'] => " ", // Space needed to separate backtick.
                    &[b' ', .., b' '] => " ",         // Space needed to escape inner space.
                    _ => "",                          // No space needed.
                };
                write!(formatter, "{backticks}{space}{text}{space}{backticks}")
            }
        }
        Start(tag) => {
            if let List(list_type) = tag {
                state.list_stack.push(*list_type);
                if state.list_stack.len() > 1 && state.newlines_before_start < options.newlines_after_rest {
                    state.newlines_before_start = options.newlines_after_rest;
                }
            }
            let consumed_newlines = state.newlines_before_start != 0;
            consume_newlines(formatter, state)?;
            match tag {
                Item => {
                    // lazy lists act like paragraphs with no event
                    state.last_was_paragraph_start = true;
                    match state.list_stack.last_mut() {
                        Some(inner) => {
                            state.padding.push(padding_of(*inner));
                            match inner {
                                Some(n) => {
                                    let bullet_number = *n;
                                    if options.increment_ordered_list_bullets {
                                        *n += 1;
                                    }
                                    write!(formatter, "{}{} ", bullet_number, options.ordered_list_token)
                                }
                                None => write!(formatter, "{} ", options.list_token),
                            }
                        }
                        None => Ok(()),
                    }
                }
                Table(alignments) => {
                    state.table_alignments = alignments.iter().map(From::from).collect();
                    Ok(())
                }
                TableHead => Ok(()),
                TableRow => Ok(()),
                TableCell => {
                    state.text_for_header = Some(String::new());
                    state.in_table_cell = true;
                    formatter.write_char('|')
                }
                Link {
                    link_type,
                    dest_url,
                    title,
                    id,
                } => {
                    state.link_stack.push(match link_type {
                        LinkType::Autolink | LinkType::Email => {
                            formatter.write_char('<')?;
                            LinkCategory::AngleBracketed
                        }
                        LinkType::Reference => {
                            formatter.write_char('[')?;
                            LinkCategory::Reference {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                                id: id.clone().into(),
                            }
                        }
                        LinkType::Collapsed => {
                            state.current_shortcut_text = Some(String::new());
                            formatter.write_char('[')?;
                            LinkCategory::Collapsed {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                            }
                        }
                        LinkType::Shortcut => {
                            state.current_shortcut_text = Some(String::new());
                            formatter.write_char('[')?;
                            LinkCategory::Shortcut {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                            }
                        }
                        _ => {
                            formatter.write_char('[')?;
                            LinkCategory::Other {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                            }
                        }
                    });
                    Ok(())
                }
                Image {
                    link_type,
                    dest_url,
                    title,
                    id,
                } => {
                    state.image_stack.push(match link_type {
                        LinkType::Reference => ImageLink::Reference {
                            uri: dest_url.clone().into(),
                            title: title.clone().into(),
                            id: id.clone().into(),
                        },
                        LinkType::Collapsed => {
                            state.current_shortcut_text = Some(String::new());
                            ImageLink::Collapsed {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                            }
                        }
                        LinkType::Shortcut => {
                            state.current_shortcut_text = Some(String::new());
                            ImageLink::Shortcut {
                                uri: dest_url.clone().into(),
                                title: title.clone().into(),
                            }
                        }
                        _ => ImageLink::Other {
                            uri: dest_url.clone().into(),
                            title: title.clone().into(),
                        },
                    });
                    formatter.write_str("![")
                }
                Emphasis => formatter.write_char(options.emphasis_token),
                Strong => formatter.write_str(options.strong_token),
                FootnoteDefinition(name) => {
                    state.padding.push("    ".into());
                    write!(formatter, "[^{name}]: ")
                }
                Paragraph => {
                    state.last_was_paragraph_start = true;
                    Ok(())
                }
                Heading {
                    level,
                    id,
                    classes,
                    attrs,
                } => {
                    if state.current_heading.is_some() {
                        return Err(Error::UnexpectedEvent);
                    }
                    state.current_heading = Some(self::Heading {
                        id: id.as_ref().map(|id| id.clone().into()),
                        classes: classes.iter().map(|class| class.clone().into()).collect(),
                        attributes: attrs
                            .iter()
                            .map(|(k, v)| (k.clone().into(), v.as_ref().map(|val| val.clone().into())))
                            .collect(),
                    });
                    match level {
                        HeadingLevel::H1 => formatter.write_str("#"),
                        HeadingLevel::H2 => formatter.write_str("##"),
                        HeadingLevel::H3 => formatter.write_str("###"),
                        HeadingLevel::H4 => formatter.write_str("####"),
                        HeadingLevel::H5 => formatter.write_str("#####"),
                        HeadingLevel::H6 => formatter.write_str("######"),
                    }?;
                    formatter.write_char(' ')
                }
                BlockQuote(kind) => {
                    let every_line_padding = " > ";
                    let first_line_padding = kind
                        .map(|kind| match kind {
                            BlockQuoteKind::Note => " > [!NOTE]",
                            BlockQuoteKind::Tip => " > [!TIP]",
                            BlockQuoteKind::Important => " > [!IMPORTANT]",
                            BlockQuoteKind::Warning => " > [!WARNING]",
                            BlockQuoteKind::Caution => " > [!CAUTION]",
                        })
                        .unwrap_or(every_line_padding);
                    state.newlines_before_start = 1;

                    // if we consumed some newlines, we know that we can just write out the next
                    // level in our blockquote. This should work regardless if we have other
                    // padding or if we're in a list
                    if !consumed_newlines {
                        write_padded_newline(formatter, state)?;
                    }
                    formatter.write_str(first_line_padding)?;
                    state.padding.push(every_line_padding.into());
                    Ok(())
                }
                CodeBlock(pulldown_cmark::CodeBlockKind::Indented) => {
                    state.code_block = Some(CodeBlockKind::Indented);
                    state.padding.push("    ".into());
                    if consumed_newlines {
                        formatter.write_str("    ")
                    } else {
                        write_padded_newline(formatter, &state)
                    }
                }
                CodeBlock(pulldown_cmark::CodeBlockKind::Fenced(info)) => {
                    state.code_block = Some(CodeBlockKind::Fenced);
                    let s = if consumed_newlines {
                        Ok(())
                    } else {
                        write_padded_newline(formatter, &state)
                    };

                    s.and_then(|()| {
                        for _ in 0..options.code_block_token_count {
                            formatter.write_char(options.code_block_token)?;
                        }
                        Ok(())
                    })
                    .and_then(|()| formatter.write_str(info))?;
                    write_padded_newline(formatter, &state)
                }
                HtmlBlock => Ok(()),
                MetadataBlock(MetadataBlockKind::YamlStyle) => formatter.write_str("---\n"),
                MetadataBlock(MetadataBlockKind::PlusesStyle) => formatter.write_str("+++\n"),
                List(_) => Ok(()),
                Strikethrough => formatter.write_str("~~"),
                DefinitionList => Ok(()),
                DefinitionListTitle => {
                    if state.newlines_before_start < options.newlines_after_rest {
                        state.newlines_before_start = options.newlines_after_rest;
                    }
                    Ok(())
                }
                DefinitionListDefinition => {
                    let every_line_padding = "  ";
                    let first_line_padding = ": ";

                    padding(formatter, &state.padding).and(formatter.write_str(first_line_padding))?;
                    state.padding.push(every_line_padding.into());
                    Ok(())
                }
                Superscript => formatter.write_str("<sup>"),
                Subscript => formatter.write_str("<sub>"),
            }
        }
        End(tag) => match tag {
            TagEnd::Link => match if let Some(link_cat) = state.link_stack.pop() {
                link_cat
            } else {
                return Err(Error::UnexpectedEvent);
            } {
                LinkCategory::AngleBracketed => formatter.write_char('>'),
                LinkCategory::Reference { uri, title, id } => {
                    state
                        .shortcuts
                        .push((id.to_string(), uri.to_string(), title.to_string()));
                    formatter.write_str("][")?;
                    formatter.write_str(&id)?;
                    formatter.write_char(']')
                }
                LinkCategory::Collapsed { uri, title } => {
                    if let Some(shortcut_text) = state.current_shortcut_text.take() {
                        state
                            .shortcuts
                            .push((shortcut_text, uri.to_string(), title.to_string()));
                    }
                    formatter.write_str("][]")
                }
                LinkCategory::Shortcut { uri, title } => {
                    if let Some(shortcut_text) = state.current_shortcut_text.take() {
                        state
                            .shortcuts
                            .push((shortcut_text, uri.to_string(), title.to_string()));
                    }
                    formatter.write_char(']')
                }
                LinkCategory::Other { uri, title } => close_link(&uri, &title, formatter, LinkType::Inline),
            },
            TagEnd::Image => match if let Some(img_link) = state.image_stack.pop() {
                img_link
            } else {
                return Err(Error::UnexpectedEvent);
            } {
                ImageLink::Reference { uri, title, id } => {
                    state
                        .shortcuts
                        .push((id.to_string(), uri.to_string(), title.to_string()));
                    formatter.write_str("][")?;
                    formatter.write_str(&id)?;
                    formatter.write_char(']')
                }
                ImageLink::Collapsed { uri, title } => {
                    if let Some(shortcut_text) = state.current_shortcut_text.take() {
                        state
                            .shortcuts
                            .push((shortcut_text, uri.to_string(), title.to_string()));
                    }
                    formatter.write_str("][]")
                }
                ImageLink::Shortcut { uri, title } => {
                    if let Some(shortcut_text) = state.current_shortcut_text.take() {
                        state
                            .shortcuts
                            .push((shortcut_text, uri.to_string(), title.to_string()));
                    }
                    formatter.write_char(']')
                }
                ImageLink::Other { uri, title } => {
                    close_link(uri.as_ref(), title.as_ref(), formatter, LinkType::Inline)
                }
            },
            TagEnd::Emphasis => formatter.write_char(options.emphasis_token),
            TagEnd::Strong => formatter.write_str(options.strong_token),
            TagEnd::Heading(_) => {
                let Some(self::Heading {
                    id,
                    classes,
                    attributes,
                }) = state.current_heading.take()
                else {
                    return Err(Error::UnexpectedEvent);
                };
                let emit_braces = id.is_some() || !classes.is_empty() || !attributes.is_empty();
                if emit_braces {
                    formatter.write_str(" {")?;
                }
                if let Some(id_str) = id {
                    formatter.write_char(' ')?;
                    formatter.write_char('#')?;
                    formatter.write_str(&id_str)?;
                }
                for class in &classes {
                    formatter.write_char(' ')?;
                    formatter.write_char('.')?;
                    formatter.write_str(class)?;
                }
                for (key, val) in &attributes {
                    formatter.write_char(' ')?;
                    formatter.write_str(key)?;
                    if let Some(val) = val {
                        formatter.write_char('=')?;
                        formatter.write_str(val)?;
                    }
                }
                if emit_braces {
                    formatter.write_char(' ')?;
                    formatter.write_char('}')?;
                }
                if state.newlines_before_start < options.newlines_after_headline {
                    state.newlines_before_start = options.newlines_after_headline;
                }
                Ok(())
            }
            TagEnd::Paragraph => {
                if state.newlines_before_start < options.newlines_after_paragraph {
                    state.newlines_before_start = options.newlines_after_paragraph;
                }
                Ok(())
            }
            TagEnd::CodeBlock => {
                if state.newlines_before_start < options.newlines_after_codeblock {
                    state.newlines_before_start = options.newlines_after_codeblock;
                }
                if last_was_text_without_trailing_newline {
                    write_padded_newline(formatter, &state)?;
                }
                match state.code_block {
                    Some(CodeBlockKind::Fenced) => {
                        for _ in 0..options.code_block_token_count {
                            formatter.write_char(options.code_block_token)?;
                        }
                    }
                    Some(CodeBlockKind::Indented) => {
                        state.padding.pop();
                    }
                    None => {}
                }
                state.code_block = None;
                Ok(())
            }
            TagEnd::HtmlBlock => {
                if state.newlines_before_start < options.newlines_after_htmlblock {
                    state.newlines_before_start = options.newlines_after_htmlblock;
                }
                Ok(())
            }
            TagEnd::MetadataBlock(MetadataBlockKind::PlusesStyle) => {
                if state.newlines_before_start < options.newlines_after_metadata {
                    state.newlines_before_start = options.newlines_after_metadata;
                }
                formatter.write_str("+++\n")
            }
            TagEnd::MetadataBlock(MetadataBlockKind::YamlStyle) => {
                if state.newlines_before_start < options.newlines_after_metadata {
                    state.newlines_before_start = options.newlines_after_metadata;
                }
                formatter.write_str("---\n")
            }
            TagEnd::Table => {
                if state.newlines_before_start < options.newlines_after_table {
                    state.newlines_before_start = options.newlines_after_table;
                }
                state.table_alignments.clear();
                state.table_headers.clear();
                Ok(())
            }
            TagEnd::TableCell => {
                state
                    .table_headers
                    .push(state.text_for_header.take().unwrap_or_default());
                state.in_table_cell = false;
                Ok(())
            }
            t @ (TagEnd::TableRow | TagEnd::TableHead) => {
                if state.newlines_before_start < options.newlines_after_rest {
                    state.newlines_before_start = options.newlines_after_rest;
                }
                formatter.write_char('|')?;

                if let TagEnd::TableHead = t {
                    write_padded_newline(formatter, &state)?;
                    for (alignment, name) in state.table_alignments.iter().zip(state.table_headers.iter()) {
                        formatter.write_char('|')?;
                        // NOTE: For perfect counting, count grapheme clusters.
                        // The reason this is not done is to avoid the dependency.

                        // The minimum width of the column so that we can represent its alignment.
                        let min_width = match alignment {
                            // Must at least represent `-`.
                            Alignment::None => 1,
                            // Must at least represent `:-` or `-:`
                            Alignment::Left | Alignment::Right => 2,
                            // Must at least represent `:-:`
                            Alignment::Center => 3,
                        };
                        let length = name.chars().count().max(min_width);
                        let last_minus_one = length.saturating_sub(1);
                        for c in 0..length {
                            formatter.write_char(
                                if (c == 0 && (alignment == &Alignment::Center || alignment == &Alignment::Left))
                                    || (c == last_minus_one
                                        && (alignment == &Alignment::Center || alignment == &Alignment::Right))
                                {
                                    ':'
                                } else {
                                    '-'
                                },
                            )?;
                        }
                    }
                    formatter.write_char('|')?;
                }
                Ok(())
            }
            TagEnd::Item => {
                state.padding.pop();
                if state.newlines_before_start < options.newlines_after_rest {
                    state.newlines_before_start = options.newlines_after_rest;
                }
                Ok(())
            }
            TagEnd::List(_) => {
                state.list_stack.pop();
                if state.list_stack.is_empty() && state.newlines_before_start < options.newlines_after_list {
                    state.newlines_before_start = options.newlines_after_list;
                }
                Ok(())
            }
            TagEnd::BlockQuote(_) => {
                state.padding.pop();

                if state.newlines_before_start < options.newlines_after_blockquote {
                    state.newlines_before_start = options.newlines_after_blockquote;
                }

                Ok(())
            }
            TagEnd::FootnoteDefinition => {
                state.padding.pop();
                Ok(())
            }
            TagEnd::Strikethrough => formatter.write_str("~~"),
            TagEnd::DefinitionList => {
                if state.newlines_before_start < options.newlines_after_list {
                    state.newlines_before_start = options.newlines_after_list;
                }
                Ok(())
            }
            TagEnd::DefinitionListTitle => formatter.write_char('\n'),
            TagEnd::DefinitionListDefinition => {
                state.padding.pop();
                write_padded_newline(formatter, &state)
            }
            TagEnd::Superscript => formatter.write_str("</sup>"),
            TagEnd::Subscript => formatter.write_str("</sub>"),
        },
        HardBreak => formatter.write_str("  ").and(write_padded_newline(formatter, &state)),
        SoftBreak => write_padded_newline(formatter, &state),
        Text(text) => {
            let mut text = &text[..];
            if let Some(shortcut_text) = state.current_shortcut_text.as_mut() {
                shortcut_text.push_str(text);
            }
            if let Some(text_for_header) = state.text_for_header.as_mut() {
                text_for_header.push_str(text);
            }
            consume_newlines(formatter, state)?;
            if last_was_paragraph_start {
                if text.starts_with('\t') {
                    formatter.write_str("&#9;")?;
                    text = &text[1..];
                } else if text.starts_with(' ') {
                    formatter.write_str("&#32;")?;
                    text = &text[1..];
                }
            }
            state.last_was_text_without_trailing_newline = !text.ends_with('\n');
            print_text_without_trailing_newline(
                &escape_special_characters(text, state, options),
                formatter,
                &state.padding,
            )
        }
        InlineHtml(text) => {
            consume_newlines(formatter, state)?;
            print_text_without_trailing_newline(text, formatter, &state.padding)
        }
        Html(text) => {
            let mut lines = text.split('\n');
            if let Some(line) = lines.next() {
                formatter.write_str(line)?;
            }
            for line in lines {
                write_padded_newline(formatter, &state)?;
                formatter.write_str(line)?;
            }
            Ok(())
        }
        FootnoteReference(name) => write!(formatter, "[^{name}]"),
        TaskListMarker(checked) => {
            let check = if *checked { "x" } else { " " };
            write!(formatter, "[{check}] ")
        }
        InlineMath(text) => write!(formatter, "${text}$"),
        DisplayMath(text) => write!(formatter, "$${text}$$"),
    };

    Ok(res?)
}

/// As [`cmark_resume_with_options()`], but with default [`Options`].
pub fn cmark_resume<'a, I, E, F>(events: I, formatter: F, state: Option<State<'a>>) -> Result<State<'a>, Error>
where
    I: Iterator<Item = E>,
    E: Borrow<Event<'a>>,
    F: fmt::Write,
{
    cmark_resume_with_options(events, formatter, state, Options::default())
}

fn close_link<F>(uri: &str, title: &str, f: &mut F, link_type: LinkType) -> fmt::Result
where
    F: fmt::Write,
{
    let needs_brackets = {
        let mut depth = 0;
        for b in uri.bytes() {
            match b {
                b'(' => depth += 1,
                b')' => depth -= 1,
                b' ' => {
                    depth += 1;
                    break;
                }
                _ => {}
            }
            if depth > 3 {
                break;
            }
        }
        depth != 0
    };
    let separator = match link_type {
        LinkType::Shortcut => ": ",
        _ => "(",
    };

    if needs_brackets {
        write!(f, "]{separator}<{uri}>")?;
    } else {
        write!(f, "]{separator}{uri}")?;
    }
    if !title.is_empty() {
        write!(f, " \"{title}\"", title = EscapeLinkTitle(title))?;
    }
    if link_type != LinkType::Shortcut {
        f.write_char(')')?;
    }

    Ok(())
}

struct EscapeLinkTitle<'a>(&'a str);

/// Writes a link title with double quotes escaped.
/// See https://spec.commonmark.org/0.30/#link-title for the rules around
/// link titles and the characters they may contain.
impl fmt::Display for EscapeLinkTitle<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for c in self.0.chars() {
            match c {
                '"' => f.write_str(r#"\""#)?,
                '\\' => f.write_str(r"\\")?,
                c => f.write_char(c)?,
            }
        }
        Ok(())
    }
}

impl State<'_> {
    pub fn finalize<F>(mut self, mut formatter: F) -> Result<Self, Error>
    where
        F: fmt::Write,
    {
        if self.shortcuts.is_empty() {
            return Ok(self);
        }

        formatter.write_str("\n")?;
        let mut written_shortcuts = HashSet::new();
        for shortcut in self.shortcuts.drain(..) {
            if written_shortcuts.contains(&shortcut) {
                continue;
            }
            write!(formatter, "\n[{}", shortcut.0)?;
            close_link(&shortcut.1, &shortcut.2, &mut formatter, LinkType::Shortcut)?;
            written_shortcuts.insert(shortcut);
        }
        Ok(self)
    }
}

/// As [`cmark_resume_with_options()`], but with the [`State`] finalized.
pub fn cmark_with_options<'a, I, E, F>(events: I, mut formatter: F, options: Options<'_>) -> Result<State<'a>, Error>
where
    I: Iterator<Item = E>,
    E: Borrow<Event<'a>>,
    F: fmt::Write,
{
    let state = cmark_resume_with_options(events, &mut formatter, Default::default(), options)?;
    state.finalize(formatter)
}

/// As [`cmark_with_options()`], but with default [`Options`].
pub fn cmark<'a, I, E, F>(events: I, mut formatter: F) -> Result<State<'a>, Error>
where
    I: Iterator<Item = E>,
    E: Borrow<Event<'a>>,
    F: fmt::Write,
{
    cmark_with_options(events, &mut formatter, Default::default())
}

/// Return the `<seen amount of consecutive fenced code-block tokens> + 1` that occur *within* a
/// fenced code-block `events`.
///
/// Use this function to obtain the correct value for `code_block_token_count` field of [`Options`]
/// to assure that the enclosing code-blocks remain functional as such.
///
/// Returns `None` if `events` didn't include any code-block, or the code-block didn't contain
/// a nested block. In that case, the correct amount of fenced code-block tokens is
/// [`DEFAULT_CODE_BLOCK_TOKEN_COUNT`].
///
/// ```rust
/// use pulldown_cmark::Event;
/// use pulldown_cmark_to_cmark::*;
///
/// let events = &[Event::Text("text".into())];
/// let code_block_token_count = calculate_code_block_token_count(events).unwrap_or(DEFAULT_CODE_BLOCK_TOKEN_COUNT);
/// let options = Options {
///     code_block_token_count,
///     ..Default::default()
/// };
/// let mut buf = String::new();
/// cmark_with_options(events.iter(), &mut buf, options);
/// ```
pub fn calculate_code_block_token_count<'a, I, E>(events: I) -> Option<usize>
where
    I: IntoIterator<Item = E>,
    E: Borrow<Event<'a>>,
{
    let mut in_codeblock = false;
    let mut max_token_count = 0;

    // token_count should be taken over Text events
    // because a continuous text may be splitted to some Text events.
    let mut token_count = 0;
    let mut prev_token_char = None;
    for event in events {
        match event.borrow() {
            Event::Start(Tag::CodeBlock(_)) => {
                in_codeblock = true;
            }
            Event::End(TagEnd::CodeBlock) => {
                in_codeblock = false;
                prev_token_char = None;
            }
            Event::Text(x) if in_codeblock => {
                for c in x.chars() {
                    let prev_token = prev_token_char.take();
                    if c == '`' || c == '~' {
                        prev_token_char = Some(c);
                        if Some(c) == prev_token {
                            token_count += 1;
                        } else {
                            max_token_count = max_token_count.max(token_count);
                            token_count = 1;
                        }
                    }
                }
            }
            _ => prev_token_char = None,
        }
    }

    max_token_count = max_token_count.max(token_count);
    (max_token_count >= 3).then_some(max_token_count + 1)
}

fn max_consecutive_chars(text: &str, search: char) -> usize {
    let mut in_search_chars = false;
    let mut max_count = 0;
    let mut cur_count = 0;

    for ch in text.chars() {
        if ch == search {
            cur_count += 1;
            in_search_chars = true;
        } else if in_search_chars {
            max_count = max_count.max(cur_count);
            cur_count = 0;
            in_search_chars = false;
        }
    }
    max_count.max(cur_count)
}

#[cfg(test)]
mod max_consecutive_chars {
    use super::max_consecutive_chars;

    #[test]
    fn happens_in_the_entire_string() {
        assert_eq!(
            max_consecutive_chars("``a```b``", '`'),
            3,
            "the highest seen consecutive segment of backticks counts"
        );
        assert_eq!(
            max_consecutive_chars("```a``b`", '`'),
            3,
            "it can't be downgraded later"
        );
    }
}
