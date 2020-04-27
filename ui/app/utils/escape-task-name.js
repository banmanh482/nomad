import emojiRegex from 'emoji-regex';

export default function escapeTaskName(taskName) {
  // Store emoji character components differently to bypass escaping:
  // "string🥳" becomes "stringUNICODE55358.UNICODE56691."
  const taskNameWithTransformedEmoji = taskName.replace(emojiRegex(), emoji => {
    return emoji.split('').map(char => {
      return `UNICODE${char.charCodeAt(0)}.`;
    }).join('');
  });

  // Regular expression is taken from here: https://stackoverflow.com/a/20053121
  const escaped = taskNameWithTransformedEmoji.replace(/[^a-zA-Z0-9,._+@%/-]/g, '\\$&');

  // Restore temporarily-transformed emoji
  return escaped.replace(/UNICODE(\d+)./g, (match, digits) => {
    return String.fromCharCode(digits);
  });
}
