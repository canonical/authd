from robot.api import logger
from robot.api.deco import keyword, library  # type: ignore


@library
class StringUtils:
    @keyword
    async def first_match(self, regex: str, text: str) -> str:
        """
        Match a regex against a string and return the first match.

        Args:
            regex: The regex to match.
            text: The text to match against.

        Returns:
            The first match.
        """
        import re

        match = re.search(regex, text)
        if match:
            logger.debug(f"Matched regex '{regex}' against text '{text}': {match.groups()}")

            # In some cases, we need to match the text with whitespaces due to OCR inaccuracies.
            # So let's ensure we remove those whitespaces from the match.
            cleaned_match = re.sub(r"\s+", "", match.group(len(match.groups())))
            return cleaned_match

        else:
            raise ValueError(f"No match found for {regex} in {text}")
