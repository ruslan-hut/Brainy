# Brainy
## Telegram Chatbot with OpenAI GPT

This project is a Telegram Chatbot that uses OpenAI's GPT language model to generate responses to user messages. The bot can be used in both private conversations and group chats.

## Requirements

To use this Chatbot, you'll need the following:

- A Telegram account
- A Telegram bot token (you can get one by talking to the BotFather)
- An OpenAI API key (you can get one by signing up for OpenAI's GPT service)

## Installation

To install and run the Chatbot, follow these steps:

1. Clone this repository to your local machine.
2. Install the required dependencies using `go get`

> go get github.com/go-telegram-bot-api/telegram-bot-api

3. Set the following environment variables in `config.yml`:

> telegram_api_key: your-Telegram-bot-token
>
> openai_api_key: your-OpenAI-API-key

4. Start the Chatbot by running the following command:

> go run main.go

## Usage

Start new chat with the bot by sending a message to it. The bot will respond with a generated message. You can also add the bot to a group chat and it will respond to messages in the group.
Bot recognizes commands in the following format:

> /ask _question_
> 
> /cat _translate word from Catalan to English_
> 
> /cas _translate word from Spanish to English_

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.

## Acknowledgements

This project was inspired by OpenAI's GPT language model and the Telegram Bot API. Thanks to both projects for their great work!
