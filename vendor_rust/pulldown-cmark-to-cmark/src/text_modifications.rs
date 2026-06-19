use super::{fmt, Cow, Options, State};

pub fn padding<F>(f: &mut F, p: &[Cow<'_, str>]) -> fmt::Result
where
    F: fmt::Write,
{
    for padding in p {
        write!(f, "{padding}")?;
    }
    Ok(())
}
pub fn consume_newlines<F>(f: &mut F, s: &mut State<'_>) -> fmt::Result
where
    F: fmt::Write,
{
    while s.newlines_before_start != 0 {
        s.newlines_before_start -= 1;
        f.write_char('\n')?;
        padding(f, &s.padding)?;
    }
    Ok(())
}

pub fn escape_special_characters<'a>(t: &'a str, state: &State<'a>, options: &Options<'a>) -> Cow<'a, str> {
    if state.is_in_code_block() || t.is_empty() {
        return Cow::Borrowed(t);
    }

    let first = t.chars().next().expect("at least one char");
    let first_special = options.special_characters().contains(first);
    let ends_with_special =
        (state.next_is_link_like && t.ends_with("!")) || (state.current_heading.is_some() && t.ends_with("#"));
    let table_contains_pipe = !state.table_alignments.is_empty() && t.contains("|");
    if first_special || ends_with_special || table_contains_pipe {
        let mut s = String::with_capacity(t.len() + 1);
        for (i, c) in t.char_indices() {
            if (i == 0 && first_special) || (i == t.len() - 1 && ends_with_special) || (c == '|' && table_contains_pipe)
            {
                s.push('\\');
            }
            s.push(c);
        }
        Cow::Owned(s)
    } else {
        Cow::Borrowed(t)
    }
}

pub fn print_text_without_trailing_newline<F>(t: &str, f: &mut F, p: &[Cow<'_, str>]) -> fmt::Result
where
    F: fmt::Write,
{
    let line_count = t.split('\n').count();
    for (tid, token) in t.split('\n').enumerate() {
        f.write_str(token)?;
        if tid + 1 < line_count {
            f.write_char('\n')?;
            padding(f, p)?;
        }
    }
    Ok(())
}

pub fn padding_of(l: Option<u64>) -> Cow<'static, str> {
    match l {
        None => "  ".into(),
        Some(n) => format!("{n}. ").chars().map(|_| ' ').collect::<String>().into(),
    }
}

/// Write a newline followed by the current [`State::padding`]
/// text that indents the current nested content.
///
/// [`write_padded_newline()`] takes care of writing both a newline character,
/// and the appropriate padding characters, abstracting over the need to
/// carefully pair those actions.
///
/// # Purpose
///
/// Consider a scenario where we're trying to write out the following Markdown
/// (space indents visualized as '·'):
///
/// ```markdown
/// >·A block quote with an embedded list:
/// >·
/// >·* This is a list item that itself contains
/// >···multiple lines and paragraphs of content.
/// >···
/// >···Second paragraph.
/// ```
///
/// Each line of output within the block quote needs to include the text `">·"`
/// at the beginning of the line. Additionally, within the list, each line
/// _also_ needs to start with `"··"` spaces so that the content of the
/// list item is indented.
///
/// Concretely, a call to [`write_padded_newline()`] after the first line in the
/// paragraph of the list item would write `"\n>···"`.
pub(crate) fn write_padded_newline(formatter: &mut impl fmt::Write, state: &State<'_>) -> Result<(), fmt::Error> {
    formatter.write_char('\n')?;
    padding(formatter, &state.padding)?;
    Ok(())
}
